package util

import (
	"kubevirt.io/kubevirt/pkg/virt-launcher/virtwrap/api"
)

// GetVolumeNameByTarget returns the volume name associated to the device target in the domain (e.g vda)
func GetVolumeNameByTarget(domain *api.Domain, target string) string {
	for _, d := range domain.Spec.Devices.Disks {
		if d.Target.Device == target {
			return d.Alias.Name
		}
	}
	return ""
}
