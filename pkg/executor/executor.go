package executor

import (
	"github.com/Azure/Orkestra/api/v1alpha1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"os"

	v1alpha13 "github.com/argoproj/argo-workflows/v3/pkg/apis/workflow/v1alpha1"
)

// Action defines the set of executor actions which can be performed on a helmrelease object
type Action string

const (
	Install Action = "install"
	Delete  Action = "delete"
)

const (
	DefaultTimeout = "5m"
	ExecutorName   = "executor"

	HelmReleaseArg = "helmrelease"
	TimeoutArg     = "timeout"
)

func workflowServiceAccountName() string {
	if sa, ok := os.LookupEnv("WORKFLOW_SERVICEACCOUNT_NAME"); ok {
		return sa
	}
	return "orkestra"
}

type Executor interface {
	GetName() string
	Reverse() Executor
	GetTemplate() v1alpha13.Template
	GetTask(name string, dependencies []string, timeout, hrStr string, parameters *apiextensionsv1.JSON) (v1alpha13.DAGTask, error)
}

func ForwardFactory(executorType v1alpha1.ExecutorType) Executor {
	switch executorType {
	case v1alpha1.KeptnExecutor:
		return KeptnForward{}
	default:
		return HelmReleaseForward{}
	}
}
