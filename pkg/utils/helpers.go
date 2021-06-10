package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	fluxhelmv2beta1 "github.com/fluxcd/helm-controller/api/v2beta1"
	"helm.sh/helm/v3/pkg/chart"
	"sigs.k8s.io/yaml"
)

func ConvertToDNS1123(in string) string {
	return strings.ReplaceAll(in, "_", "-")
}

func ConvertSliceToDNS1123(in []string) []string {
	out := []string{}
	for _, s := range in {
		out = append(out, ConvertToDNS1123(s))
	}
	return out
}

func GetHash(in string) string {
	h := sha256.New()
	h.Write([]byte(in))
	return hex.EncodeToString(h.Sum(nil))
}

func TruncateString(in string, num int) string {
	out := in
	if len(in) > num {
		out = in[0:num]
	}
	return out
}

func ToStrPtr(in string) *string {
	return &in
}

func HrToYaml(hr fluxhelmv2beta1.HelmRelease) string {
	b, err := yaml.Marshal(hr)
	if err != nil {
		return ""
	}

	return string(b)
}

func TemplateContainsYaml(ch *chart.Chart) (bool, error) {
	if ch == nil {
		return false, fmt.Errorf("chart cannot be nil")
	}

	for _, f := range ch.Templates {
		if IsFileYaml(f.Name) {
			return true, nil
		}
	}
	return false, nil
}

func IsFileYaml(f string) bool {
	f = strings.ToLower(f)
	if strings.HasSuffix(f, "yml") || strings.HasSuffix(f, "yaml") {
		return true
	}
	return false
}

func AddAppChartNameToFile(name, a string) string {
	prefix := "templates/"
	name = strings.TrimPrefix(name, prefix)
	name = a + "_" + name
	return prefix + name
}