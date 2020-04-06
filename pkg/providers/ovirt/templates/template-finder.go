package templates

import (
	"fmt"
	"strings"

	templatev1 "github.com/openshift/api/template/v1"
	ovirtsdk "github.com/ovirt/go-ovirt"
	"k8s.io/apimachinery/pkg/runtime"
	kubevirtv1 "kubevirt.io/client-go/api/v1"
)

const (
	// TemplateNamespace stores the default namespace for kubevirt templates
	TemplateNamespace = "openshift"
	defaultLinux      = "rhel8"
	defaultWindows    = "windows"
	defaultFlavor     = "medium"
)

var osInfo = map[string]string{
	"Red Hat Enterprise Linux Server": "rhel",
	// TODO add more
}

// TemplateFinder attempts to find a template based on given parameters
type TemplateFinder struct {
	provider TemplateProvider
}

// TemplateProvider searches for template in Openshift
type TemplateProvider interface {
	Find(namespace *string,
		os *string,
		workload *string,
		flavor *string,
	) (*templatev1.TemplateList, error)

	Process(namespace string, vmName string, template *templatev1.Template) (*templatev1.Template, error)
}

// NewTemplateFinder creates new TemplateFinder
func NewTemplateFinder(provider TemplateProvider) *TemplateFinder {
	return &TemplateFinder{
		provider: provider,
	}
}

// FindTemplate attempts to find best match for a template based on the source VM
func (f *TemplateFinder) FindTemplate(vm *ovirtsdk.Vm) (*templatev1.Template, error) {
	os := findOperatingSystem(vm)
	workload := getWorkload(vm)
	return f.getTemplate(os, workload)
}

func findOperatingSystem(vm *ovirtsdk.Vm) string {
	if gos, found := vm.GuestOperatingSystem(); found {
		distribution, _ := gos.Distribution()
		version, _ := gos.Version()
		fullVersion, _ := version.FullVersion()
		return fmt.Sprintf("%s%s", osInfo[distribution], fullVersion)
	}
	if os, found := vm.Os(); found {
		osType, _ := os.Type()
		// limit number of possibilities
		osType = strings.ToLower(osType)
		if strings.Contains(osType, "linux") || strings.Contains(osType, "rhel") {
			return defaultLinux
		} else if strings.Contains(osType, "win") {
			return defaultWindows
		}
	}
	// return empty to fail label selector
	return ""
}

func getWorkload(vm *ovirtsdk.Vm) string {
	// vm type is always available
	vmType, _ := vm.Type()
	// we need to remove underscore from high_performance, other workloads are OK
	return strings.Replace(string(vmType), "_", "", -1)
}

func (f *TemplateFinder) getTemplate(os string, workload string) (*templatev1.Template, error) {
	// We update metadata from the source vm so we default to medium flavor
	namespace := TemplateNamespace
	flavor := defaultFlavor
	templates, err := f.provider.Find(&namespace, &os, &workload, &flavor)
	if err != nil {
		return nil, err
	}
	if len(templates.Items) == 0 {
		return nil, fmt.Errorf("Template not found for %s OS and %s workload", os, workload)
	}
	// Take first which matches label selector
	return &templates.Items[0], nil
}

func (f *TemplateFinder) processTemplate(template *templatev1.Template, vmName string) (*kubevirtv1.VirtualMachine, error) {
	processed, err := f.provider.Process(TemplateNamespace, vmName, template)
	if err != nil {
		return nil, err
	}
	var vm = &kubevirtv1.VirtualMachine{}
	for _, obj := range processed.Objects {
		decoder := kubevirtv1.Codecs.UniversalDecoder(kubevirtv1.GroupVersion)
		decoded, err := runtime.Decode(decoder, obj.Raw)
		if err != nil {
			return nil, err
		}
		done, ok := decoded.(*kubevirtv1.VirtualMachine)
		if ok {
			vm = done
			break
		}
	}
	vm.Spec.Template.Spec.Volumes = []kubevirtv1.Volume{}
	vm.Spec.Template.Spec.Networks = []kubevirtv1.Network{}
	return vm, nil
}
