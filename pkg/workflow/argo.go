package workflow

import (
	"context"
	kerrors "errors"
	"fmt"
	"os"

	"github.com/Azure/Orkestra/api/v1alpha1"
	"github.com/Azure/Orkestra/pkg"
	v1alpha12 "github.com/argoproj/argo/pkg/apis/workflow/v1alpha1"
	helmopv1 "github.com/fluxcd/helm-operator/pkg/apis/helm.fluxcd.io/v1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"
)

const (
	argoAPIVersion    = "argoproj.io/v1alpha1"
	argoKind          = "Workflow"
	entrypointTplName = "entry"

	helmReleaseArg      = "helmrelease"
	helmReleaseExecutor = "helmrelease-executor"

	valuesKeyGlobal = "global"
	ChartLabelKey   = "chart"
)

var (
	ErrNamespaceTerminating       = kerrors.New("namespace is in terminating phase")
	timeout                 int64 = 3600
)

type argo struct {
	scheme *runtime.Scheme
	cli    client.Client
	wf     *v1alpha12.Workflow

	stagingRepoURL string
}

// Argo implements the Workflow interface for the Argo Workflow based DAG engine
func Argo(scheme *runtime.Scheme, c client.Client, stagingRepoURL string) *argo { //nolint:golint
	return &argo{
		scheme:         scheme,
		cli:            c,
		stagingRepoURL: stagingRepoURL,
	}
}

func (a *argo) initWorkflowObject() {
	a.wf = &v1alpha12.Workflow{
		ObjectMeta: v1.ObjectMeta{
			Labels: make(map[string]string),
		},
	}

	a.wf.Labels[HeritageLabel] = Project

	a.wf.APIVersion = argoAPIVersion
	a.wf.Kind = argoKind

	// Entry point is the entry node into the Application Group DAG
	a.wf.Spec.Entrypoint = entrypointTplName

	// Initialize the Templates slice
	a.wf.Spec.Templates = make([]v1alpha12.Template, 0)
}

func (a *argo) Generate(ctx context.Context, l logr.Logger, ns string, g *v1alpha1.ApplicationGroup) error {
	if g == nil {
		l.Error(nil, "ApplicationGroup object cannot be nil")
		return fmt.Errorf("applicationGroup object cannot be nil")
	}

	a.initWorkflowObject()

	// Set name and namespace based on the input application group
	a.wf.Name = g.Name
	a.wf.Namespace = ns

	err := a.generateWorkflow(ctx, g)
	if err != nil {
		l.Error(err, "failed to generate workflow")
		return fmt.Errorf("failed to generate argo workflow : %w", err)
	}

	return nil
}

func (a *argo) Submit(ctx context.Context, l logr.Logger, g *v1alpha1.ApplicationGroup) error {
	if a.wf == nil {
		l.Error(nil, "workflow object cannot be nil")
		return fmt.Errorf("workflow object cannot be nil")
	}

	if g == nil {
		l.Error(nil, "applicationGroup object cannot be nil")
		return fmt.Errorf("applicationGroup object cannot be nil")
	}

	obj := &v1alpha12.Workflow{
		ObjectMeta: v1.ObjectMeta{Labels: make(map[string]string)},
	}

	namespaces := []string{}
	for _, app := range g.Spec.Applications {
		namespaces = append(namespaces, app.Spec.TargetNamespace)
	}

	for _, namespace := range namespaces {
		ns := corev1.Namespace{
			TypeMeta: v1.TypeMeta{
				Kind:       "Namespace",
				APIVersion: "v1",
			},
			ObjectMeta: v1.ObjectMeta{
				Name: namespace,
			},
		}
		// Create the namespace since helm-operator does not do this
		// FIXME (nitishm) Handle namespace in termination state by requeueing
		err := a.cli.Get(ctx, types.NamespacedName{Name: ns.Name}, &ns)
		if err != nil {
			if errors.IsNotFound(err) {
				// Add OwnershipReference
				err = controllerutil.SetControllerReference(g, &ns, a.scheme)
				if err != nil {
					return fmt.Errorf("failed to set OwnerReference for Namespace %s : %w", ns.Name, err)
				}

				err = a.cli.Create(ctx, &ns)
				if err != nil {
					return fmt.Errorf("failed to CREATE namespace %s object : %w", ns.Name, err)
				}
			}
		}

		if ns.Status.Phase == corev1.NamespaceTerminating {
			return ErrNamespaceTerminating
		}
	}

	err := a.cli.Get(ctx, types.NamespacedName{Namespace: a.wf.Namespace, Name: a.wf.Name}, obj)
	if err != nil {
		if errors.IsNotFound(err) {
			// Add OwnershipReference
			err = controllerutil.SetControllerReference(g, a.wf, a.scheme)
			if err != nil {
				l.Error(err, "unable to set ApplicationGroup as owner of Argo Workflow object")
				return fmt.Errorf("unable to set ApplicationGroup as owner of Argo Workflow: %w", err)
			}

			a.wf.Labels[OwnershipLabel] = g.Name

			// If the argo Workflow object is NotFound and not AlreadyExists on the cluster
			// create a new object and submit it to the cluster
			err = a.cli.Create(ctx, a.wf)
			if err != nil {
				l.Error(err, "failed to CREATE argo workflow object")
				return fmt.Errorf("failed to CREATE argo workflow object : %w", err)
			}
		} else {
			l.Error(err, "failed to GET workflow object with an unrecoverable error")
			return fmt.Errorf("failed to GET workflow object with an unrecoverable error : %w", err)
		}
	}

	// If the workflow needs an update, delete the previous workflow and apply the new one
	// Argo Workflow does not rerun the workflow on UPDATE, so intead we cleanup and reapply
	if g.Status.Update {
		err = a.cli.Delete(ctx, obj)
		if err != nil {
			l.Error(err, "failed to DELETE argo workflow object")
			return fmt.Errorf("failed to DELETE argo workflow object : %w", err)
		}
		// If the argo Workflow object is Found on the cluster
		// update the workflow and submit it to the cluster
		// Add OwnershipReference
		err = controllerutil.SetControllerReference(g, a.wf, a.scheme)
		if err != nil {
			l.Error(err, "unable to set ApplicationGroup as owner of Argo Workflow object")
			return fmt.Errorf("unable to set ApplicationGroup as owner of Argo Workflow: %w", err)
		}

		a.wf.Labels[OwnershipLabel] = g.Name

		// If the argo Workflow object is NotFound and not AlreadyExists on the cluster
		// create a new object and submit it to the cluster
		err = a.cli.Create(ctx, a.wf)
		if err != nil {
			l.Error(err, "failed to CREATE argo workflow object")
			return fmt.Errorf("failed to CREATE argo workflow object : %w", err)
		}

		g.Status.Update = false
	}

	return nil
}

func (a *argo) generateWorkflow(ctx context.Context, g *v1alpha1.ApplicationGroup) error {
	// Generate the Entrypoint template and Application Group DAG
	err := a.generateAppGroupTpls(ctx, g)
	if err != nil {
		return fmt.Errorf("failed to generate Application Group DAG : %w", err)
	}

	return nil
}

func (a *argo) generateAppGroupTpls(ctx context.Context, g *v1alpha1.ApplicationGroup) error {
	if a.wf == nil {
		return fmt.Errorf("workflow cannot be nil")
	}

	if g == nil {
		return fmt.Errorf("applicationGroup cannot be nil")
	}

	entry := v1alpha12.Template{
		Name: entrypointTplName,
		DAG: &v1alpha12.DAGTemplate{
			Tasks: make([]v1alpha12.DAGTask, len(g.Spec.Applications)),
			// TBD (nitishm): Do we need to failfast?
			// FailFast: true
		},
	}

	adt, err := a.generateAppDAGTemplates(ctx, g, a.stagingRepoURL)
	if err != nil {
		return fmt.Errorf("failed to generate application DAG templates : %w", err)
	}
	a.updateWorkflowTemplates(adt...)

	err = updateAppGroupDAG(g, &entry, adt)
	if err != nil {
		return fmt.Errorf("failed to generate Application Group DAG : %w", err)
	}
	a.updateWorkflowTemplates(entry)

	// TODO: Add the executor template
	// This should eventually be configurable
	a.updateWorkflowTemplates(defaultExecutor())

	return nil
}

func updateAppGroupDAG(g *v1alpha1.ApplicationGroup, entry *v1alpha12.Template, tpls []v1alpha12.Template) error {
	if entry == nil {
		return fmt.Errorf("entry template cannot be nil")
	}

	entry.DAG = &v1alpha12.DAGTemplate{
		Tasks: make([]v1alpha12.DAGTask, len(tpls), len(tpls)),
	}

	for i, tpl := range tpls {
		entry.DAG.Tasks[i] = v1alpha12.DAGTask{
			Name:         pkg.ConvertToDNS1123(tpl.Name),
			Template:     pkg.ConvertToDNS1123(tpl.Name),
			Dependencies: convertSliceToDNS1123(g.Spec.Applications[i].Dependencies),
		}
	}

	return nil
}

func (a *argo) generateAppDAGTemplates(ctx context.Context, g *v1alpha1.ApplicationGroup, repo string) ([]v1alpha12.Template, error) {
	ts := make([]v1alpha12.Template, 0)

	for i, app := range g.Spec.Applications {
		var hasSubcharts bool
		app.Spec.Values = app.Spec.Overlays

		appStatus := &g.Status.Applications[i].ChartStatus
		scStatus := g.Status.Applications[i].Subcharts

		// Create Subchart DAG only when the application chart has dependencies
		if len(app.Spec.Subcharts) > 0 {
			hasSubcharts = true
			t := v1alpha12.Template{
				Name: pkg.ConvertToDNS1123(app.Name),
			}

			t.DAG = &v1alpha12.DAGTemplate{}
			tasks, err := a.generateSubchartAndAppDAGTasks(ctx, g, &app, appStatus, scStatus, repo, app.Spec.HelmReleaseSpec.TargetNamespace)
			if err != nil {
				return nil, fmt.Errorf("failed to generate Application Template DAG tasks : %w", err)
			}

			t.DAG.Tasks = tasks

			ts = append(ts, t)
		}

		if !hasSubcharts {
			hr := helmopv1.HelmRelease{
				TypeMeta: v1.TypeMeta{
					Kind:       "HelmRelease",
					APIVersion: "helm.fluxcd.io/v1",
				},
				ObjectMeta: v1.ObjectMeta{
					Name:      pkg.ConvertToDNS1123(app.Name),
					Namespace: app.Spec.TargetNamespace,
				},
				Spec: app.DeepCopy().Spec.HelmReleaseSpec,
			}

			hr.Spec.Wait = boolToBoolPtr(true)
			hr.Spec.Timeout = &timeout
			hr.Spec.ReleaseName = pkg.ConvertToDNS1123(app.Name)

			hr.Labels = map[string]string{
				ChartLabelKey:  app.Name,
				OwnershipLabel: g.Name,
				HeritageLabel:  Project,
			}
			// hr.Spec.RepoChartSource.RepoURL = repo

			if appStatus.Staged {
				hr.Spec.RepoURL = repo
			}

			if app.Spec.DisableWait {
				hr.Spec.Wait = boolToBoolPtr(false)
			}
			tApp := v1alpha12.Template{
				Name: pkg.ConvertToDNS1123(app.Name),
				DAG: &v1alpha12.DAGTemplate{
					Tasks: []v1alpha12.DAGTask{
						{
							Name:     pkg.ConvertToDNS1123(app.Name),
							Template: helmReleaseExecutor,
							Arguments: v1alpha12.Arguments{
								Parameters: []v1alpha12.Parameter{
									{
										Name:  helmReleaseArg,
										Value: strToStrPtr(hrToYAML(hr)),
									},
								},
							},
						},
					},
				},
			}

			ts = append(ts, tApp)
		}
	}
	return ts, nil
}

func (a *argo) generateSubchartAndAppDAGTasks(ctx context.Context, g *v1alpha1.ApplicationGroup, app *v1alpha1.Application, status *v1alpha1.ChartStatus, subchartsStatus map[string]v1alpha1.ChartStatus, repo, targetNS string) ([]v1alpha12.DAGTask, error) {
	if repo == "" {
		return nil, fmt.Errorf("repo arg must be a valid non-empty string")
	}

	// XXX (nitishm) Should this be set to nil if no subcharts are found??
	tasks := make([]v1alpha12.DAGTask, 0, len(app.Spec.Subcharts)+1)

	for _, sc := range app.Spec.Subcharts {
		scName := sc.Name

		isStaged := subchartsStatus[scName].Staged
		version := subchartsStatus[scName].Version

		hr := generateSubchartHelmRelease(app.Spec.HelmReleaseSpec, app.Name, scName, version, repo, targetNS, isStaged)
		hr.Annotations = map[string]string{
			"orkestra/parent-chart": app.Name,
		}
		hr.Labels = map[string]string{
			ChartLabelKey:  app.Name,
			OwnershipLabel: g.Name,
			HeritageLabel:  Project,
		}

		if app.Spec.DisableWait {
			hr.Spec.Wait = boolToBoolPtr(false)
		}

		task := v1alpha12.DAGTask{
			Name:     pkg.ConvertToDNS1123(scName),
			Template: helmReleaseExecutor,
			Arguments: v1alpha12.Arguments{
				Parameters: []v1alpha12.Parameter{
					{
						Name:  helmReleaseArg,
						Value: strToStrPtr(hrToYAML(hr)),
					},
				},
			},
			Dependencies: convertSliceToDNS1123(sc.Dependencies),
		}

		tasks = append(tasks, task)
	}

	hr := helmopv1.HelmRelease{
		TypeMeta: v1.TypeMeta{
			Kind:       "HelmRelease",
			APIVersion: "helm.fluxcd.io/v1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      pkg.ConvertToDNS1123(app.Name),
			Namespace: app.Spec.HelmReleaseSpec.TargetNamespace,
		},
		Spec: app.Spec.DeepCopy().HelmReleaseSpec,
	}

	hr.Spec.ReleaseName = pkg.ConvertToDNS1123(app.Name)
	hr.Spec.Wait = boolToBoolPtr(true)
	hr.Spec.Timeout = &timeout

	hr.Labels = map[string]string{
		ChartLabelKey:  app.Name,
		OwnershipLabel: g.Name,
		HeritageLabel:  Project,
	}

	hr.Spec.RepoURL = repo

	if app.Spec.DisableWait {
		hr.Spec.Wait = boolToBoolPtr(false)
	}

	// Force disable all subchart for the staged application chart
	// to prevent duplication and possible collision of deployed resources
	// Since the subchart should have been deployed in a prior DAG step,
	// we must not redeploy it along with the parent application chart.
	for _, d := range app.Spec.Subcharts {
		hr.Spec.Values.Data[d.Name] = map[string]interface{}{
			"enabled": false,
		}
	}

	task := v1alpha12.DAGTask{
		Name:     pkg.ConvertToDNS1123(app.Name),
		Template: helmReleaseExecutor,
		Arguments: v1alpha12.Arguments{
			Parameters: []v1alpha12.Parameter{
				{
					Name:  helmReleaseArg,
					Value: strToStrPtr(hrToYAML(hr)),
				},
			},
		},
		Dependencies: func() (out []string) {
			for _, t := range tasks {
				out = append(out, pkg.ConvertToDNS1123(t.Name))
			}
			return out
		}(),
	}

	tasks = append(tasks, task)

	return tasks, nil
}

func (a *argo) updateWorkflowTemplates(tpls ...v1alpha12.Template) {
	a.wf.Spec.Templates = append(a.wf.Spec.Templates, tpls...)
}

func defaultExecutor() v1alpha12.Template {
	return v1alpha12.Template{
		Name:               helmReleaseExecutor,
		ServiceAccountName: defaultNamespace(),
		Inputs: v1alpha12.Inputs{
			Parameters: []v1alpha12.Parameter{
				{
					Name: "helmrelease",
				},
			},
		},
		Executor: &v1alpha12.ExecutorConfig{
			ServiceAccountName: defaultNamespace(),
		},
		Outputs: v1alpha12.Outputs{},
		Resource: &v1alpha12.ResourceTemplate{
			// SetOwnerReference: true,
			Action:           "apply",
			Manifest:         "{{inputs.parameters.helmrelease}}",
			SuccessCondition: "status.phase == Succeeded",
			FailureCondition: "status.phase == Failed",
		},
	}
}

func hrToYAML(hr helmopv1.HelmRelease) string {
	b, err := yaml.Marshal(hr)
	if err != nil {
		return ""
	}

	return string(b)
}

func generateSubchartHelmRelease(a helmopv1.HelmReleaseSpec, appName, scName, version, repo, targetNS string, isStaged bool) helmopv1.HelmRelease {
	hr := helmopv1.HelmRelease{
		TypeMeta: v1.TypeMeta{
			Kind:       "HelmRelease",
			APIVersion: "helm.fluxcd.io/v1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      pkg.ConvertToDNS1123(pkg.ToInitials(appName) + "-" + scName),
			Namespace: targetNS,
		},
		Spec: helmopv1.HelmReleaseSpec{
			ReleaseName: pkg.ConvertToDNS1123(scName),
			ChartSource: helmopv1.ChartSource{
				RepoChartSource: &helmopv1.RepoChartSource{},
			},
			TargetNamespace: targetNS,
		},
	}

	hr.Spec.Wait = boolToBoolPtr(true)
	hr.Spec.Timeout = &timeout

	// NOTE: Ownership label is added in the caller function
	hr.Spec.ChartSource.RepoChartSource = a.DeepCopy().RepoChartSource
	hr.Spec.ChartSource.RepoChartSource.Name = pkg.ConvertToDNS1123(pkg.ToInitials(appName) + "-" + scName)
	hr.Spec.ChartSource.RepoChartSource.RepoURL = repo
	hr.Spec.ChartSource.RepoChartSource.Version = version

	hr.Spec.Values = subchartValues(scName, a.Values)

	return hr
}

func subchartValues(sc string, av helmopv1.HelmValues) helmopv1.HelmValues {
	v := helmopv1.HelmValues{
		Data: make(map[string]interface{}),
	}

	if scVals, ok := av.Data[sc]; ok {
		if vv, ok := scVals.(map[string]interface{}); ok {
			for k, val := range vv {
				v.Data[k] = val
			}
		}
	}

	if gVals, ok := av.Data[valuesKeyGlobal]; ok {
		if vv, ok := gVals.(map[string]interface{}); ok {
			v.Data[valuesKeyGlobal] = vv
		}
	}

	return v
}

func defaultNamespace() string {
	if ns, ok := os.LookupEnv("WORKFLOW_NAMESPACE"); ok {
		return ns
	}
	return "orkestra"
}

func convertSliceToDNS1123(in []string) []string {
	out := []string{}
	for _, s := range in {
		out = append(out, pkg.ConvertToDNS1123(s))
	}
	return out
}

func boolToBoolPtr(in bool) *bool {
	return &in
}

func strToStrPtr(in string) *string {
	return &in
}
