package test_test

import (
	"context"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"kubevirt.io/client-go/kubecli"
	"kubevirt.io/kubevirt/tests"
)

var node string

const (
	ioerrorPV  = "ioerror-pv"
	ioerrorPVC = "ioerror-pvc"
	sc         = "test-ioerror"
	deviceName = "errdev0"
)

func createFaultyDisk() {

}

func removeFaultyDisk() {

}

func createPVCwithFaultyDisk(ns string) {
	size := resource.MustParse("1Gi")
	vMode := corev1.PersistentVolumeBlock
	affinity := corev1.VolumeNodeAffinity{
		Required: &NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{
							Key:      "kubernetes.io/hostname",
							Operator: corev1.NodeSelectorOpIn,
							Values:   []string{node},
						},
					},
				},
			},
		},
	}
	pv := corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: ioerrorPV,
		},
		Spec: corev1.PersistentVolumeSpec{
			Capacity:         map[ResourceName]resource.Quantity{corev1.ResourceStorage: size},
			StorageClassName: sc,
			VolumeMode:       &vMode,
			NodeAffinity:     &affinity,
			PersistentVolumeSource: &corev1.PersistentVolumeSource{
				Local: &corev1.LocalVolumeSource{
					Path: "/dev/mapper/" + deviceName,
				},
			},
		},
	}
	pvc := corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: ioerrorPVC,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			VolumeMode:       &vMode,
			StorageClassName: sc,
			Capacity:         map[ResourceName]resource.Quantity{corev1.ResourceStorage: size},
		},
	}
	virtCli, err := kubecli.GetKubevirtClient()
	PanicOnError(err)

	_, err = virtCli.CoreV1().PersistentVolumes(ns).Create(context.Background(), pv, metav1.CreateOptions{})
	if !errors.IsAlreadyExists(err) {
		tests.PanicOnError(err)
	}

	_, err = virtCli.CoreV1().PersistentVolumeClaims(ns).Create(context.Background(), pvc, metav1.CreateOptions{})
	if !errors.IsAlreadyExists(err) {
		tests.PanicOnError(err)
	}
}

var _ = Describe("[Serial][owner:@sig-compute]KubeVirtConfigmapConfiguration", func() {
	FIt("Should catch IO error event", func() {
		createPVCwithFaultyDisk(tests.NamespaceTestDefault)
	})
})
