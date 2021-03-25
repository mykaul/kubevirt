package guestfs

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/kubectl/pkg/cmd/attach"

	cmdutil "k8s.io/kubectl/pkg/cmd/util"

	"kubevirt.io/kubevirt/pkg/virtctl/templates"
)

const (
	defaultRegistry  = "docker.io/afrosirh"
	defaultImageName = "libguestfs-tools"
	defaultTag       = "latest"
	// KvmDevice defines the resource as in pkg/virt-controller/services/template.go, but we don't import the package to avoid compile compile conflicts when the os is windows
	KvmDevice = "devices.kubevirt.io/kvm"
)

var (
	pvc   string
	image string
)

//
type guestfsCommand struct {
	clientConfig clientcmd.ClientConfig
}

// NewGuestfsShellCommand returns a cobra.Command for starting libguestfs-tool pod and attach it to a pvc
func NewGuestfsShellCommand(clientConfig clientcmd.ClientConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "guestfs",
		Short:   "Start a shell into the libguestfs pod",
		Long:    `Create a pod with libguestfs-tools, mount the pvc and attach a shell to it. The pvc is mounted under the /disks directory inside the pod`,
		Example: usage(),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := guestfsCommand{clientConfig: clientConfig}
			return c.run(cmd, args)
		},
	}
	cmd.PersistentFlags().StringVarP(&pvc, "pvc", "p", "", "pvc claim name")
	cmd.MarkPersistentFlagRequired("pvc")
	cmd.PersistentFlags().StringVarP(&image, "image", "i", fmt.Sprintf("%s/%s:%s", defaultRegistry, defaultImageName, defaultTag), fmt.Sprintf("overwrite default container image"))
	cmd.SetUsageTemplate(templates.UsageTemplate())
	return cmd
}

func usage() string {
	usage := `  # Create a pod with libguestfs-tools, mount the pvc and attach a shell to it:
  {{ProgramName}} guestfs --pvc pvc`
	return usage
}

// ClientCreator is a function to return the Kubernetes client
type ClientCreator func(config *rest.Config) (*K8sClient, error)

var createClientFunc ClientCreator

// SetClient allows overriding the default Kubernetes client
func SetClient(f ClientCreator) {
	createClientFunc = f
}

// SetDefaulClient sets the client back to the default Kubernetes client
func SetDefaulClient() {
	createClientFunc = createClient
}

// AttachCreator is a function to attach the pod
type AttachCreator func(p *corev1.Pod, command string) error

var createAttachFunc AttachCreator

// SetAttachCreator allows overriding the default pod attacher
func SetAttachCreator(f AttachCreator) {
	createAttachFunc = f
}

// SetDefaulAttachCreator sets the default pod attacher
func SetDefaulAttachCreator() {
	createAttachFunc = createAttacher
}

func init() {
	SetDefaulClient()
	SetDefaulAttachCreator()
}

func (c *guestfsCommand) run(cmd *cobra.Command, args []string) error {
	var inUse bool
	var conf *rest.Config
	namespace, _, err := c.clientConfig.Namespace()
	if err != nil {
		return err
	}
	conf, err = c.clientConfig.ClientConfig()
	if err != nil {
		return err
	}
	client, err := createClientFunc(conf)
	if err != nil {
		return err
	}
	exist, _ := client.existsPVC(pvc, namespace)
	if !exist {
		return fmt.Errorf("The PVC %s doesn't exist", pvc)
	}
	inUse, err = client.isPVCinUse(pvc, namespace)
	if err != nil {
		return err
	}
	if inUse {
		return fmt.Errorf("PVC %s is used by another pod", pvc)
	}
	isBlock, err := client.isPVCVolumeBlock(pvc, namespace)
	if err != nil {
		return err
	}

	defer client.removePod(namespace)
	return client.createInteractivePodWithPVC(pvc, image, namespace, "/bin/bash", []string{}, isBlock)
}

var (
	volume   = "volume"
	contName = "virt"
	diskDir  = "/disks"
	diskPath = "/dev/vda"
	podName  = "libguestfs-tools"
)

var (
	timeout = 200 * time.Second
)

type K8sClient struct {
	Client kubernetes.Interface
}

func createClient(config *rest.Config) (*K8sClient, error) {
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return &K8sClient{}, err
	}
	return &K8sClient{
		Client: client,
	}, nil
}

func (client *K8sClient) existsPVC(pvc, ns string) (bool, error) {
	p, err := client.Client.CoreV1().PersistentVolumeClaims(ns).Get(context.TODO(), pvc, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	if p.Name == "" {
		return false, nil
	}
	return true, nil
}

func (client *K8sClient) isPVCVolumeBlock(pvc, ns string) (bool, error) {
	p, err := client.Client.CoreV1().PersistentVolumeClaims(ns).Get(context.TODO(), pvc, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	if *p.Spec.VolumeMode == corev1.PersistentVolumeBlock {
		return true, nil
	}
	return false, nil
}

func (client *K8sClient) existsPod(pod, ns string) bool {
	p, err := client.Client.CoreV1().Pods(ns).Get(context.TODO(), pod, metav1.GetOptions{})
	if err != nil {
		return false
	}
	if p.Name == "" {
		return false
	}
	return true
}

func (client *K8sClient) isPVCinUse(pvc, ns string) (bool, error) {
	pods, err := client.getPodsForPVC(pvc, ns)
	if err != nil {
		return false, err
	}
	if len(pods) > 0 {
		return true, nil
	}
	return false, nil
}

func (client *K8sClient) waitForContainerRunning(pod, cont, ns string, timeout time.Duration) error {
	c := make(chan string, 1)
	go func() {
		for {
			pod, err := client.Client.CoreV1().Pods(ns).Get(context.TODO(), pod, metav1.GetOptions{})
			if err != nil {
				c <- err.Error()
			}
			if pod.Status.Phase != corev1.PodPending {
				c <- string(pod.Status.Phase)

			}
			for _, c := range pod.Status.ContainerStatuses {
				if c.State.Waiting != nil {
					fmt.Printf("Waiting for container %s still in pending, reason: %s, message: %s \n", c.Name, c.State.Waiting.Reason, c.State.Waiting.Message)
				}
			}

			time.Sleep(10 * time.Millisecond)
		}
	}()
	select {
	case res := <-c:
		if res == string(corev1.PodRunning) {
			return nil
		}
		return fmt.Errorf("Pod is not in running state but got %s", res)
	case <-time.After(timeout):
		return fmt.Errorf("timeout in waiting for the containers to be started in pod %s", pod)
	}

}

func (client *K8sClient) getPodsForPVC(pvcName, ns string) ([]corev1.Pod, error) {
	nsPods, err := client.Client.CoreV1().Pods(ns).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return []corev1.Pod{}, err
	}

	var pods []corev1.Pod

	for _, pod := range nsPods.Items {
		for _, volume := range pod.Spec.Volumes {
			if volume.VolumeSource.PersistentVolumeClaim != nil && volume.VolumeSource.PersistentVolumeClaim.ClaimName == pvcName {
				pods = append(pods, pod)
			}
		}
	}

	return pods, nil
}

func createLibguestfsPod(pvc, image, cmd string, args []string, kvm, isBlock bool) *corev1.Pod {
	var resources corev1.ResourceRequirements
	var user, group int64
	user = 0
	group = 0
	if kvm {
		resources = corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				KvmDevice: resource.MustParse("1"),
			},
		}
	}
	c := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: podName,
		},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: volume,
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvc,
							ReadOnly:  false,
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:    contName,
					Image:   image,
					Command: []string{cmd},
					Args:    args,
					Env: []corev1.EnvVar{
						{
							Name:  "LIBGUESTFS_BACKEND",
							Value: "direct",
						},
					},
					ImagePullPolicy: corev1.PullIfNotPresent,
					SecurityContext: &corev1.SecurityContext{
						RunAsUser:  &user,
						RunAsGroup: &group,
					},
					Stdin:     true,
					TTY:       true,
					Resources: resources,
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}
	if isBlock {
		c.Spec.Containers[0].VolumeDevices = append(c.Spec.Containers[0].VolumeDevices, corev1.VolumeDevice{
			Name:       volume,
			DevicePath: diskPath,
		})
		fmt.Printf("The PVC has been mounted at %s \n", diskPath)
		return c
	}
	// PVC volume mode is filesystem
	c.Spec.Containers[0].VolumeMounts = append(c.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
		Name:      volume,
		ReadOnly:  false,
		MountPath: diskDir,
	})

	c.Spec.Containers[0].WorkingDir = diskDir
	fmt.Printf("The PVC has been mounted at %s \n", diskDir)

	return c
}

func createAttacher(p *corev1.Pod, command string) error {
	// Set option for attaching to the libguestfs pod
	matchVersionKubeConfigFlags := cmdutil.NewMatchVersionFlags(genericclioptions.NewConfigFlags(true))
	f := cmdutil.NewFactory(matchVersionKubeConfigFlags)
	o := attach.NewAttachOptions(genericclioptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr})
	o.Pod = p
	o.TTY = true
	o.Stdin = true
	o.CommandName = command
	o.Builder = f.NewBuilder
	o.GetPodTimeout = timeout
	config, err := f.ToRESTConfig()
	if err != nil {
		return err
	}
	o.Config = config
	return o.Run()

}

func (client *K8sClient) createInteractivePodWithPVC(pvc, image, ns, command string, args []string, isblock bool) error {
	kvm := true
	pod := createLibguestfsPod(pvc, image, command, args, kvm, isblock)
	p, err := client.Client.CoreV1().Pods(ns).Create(context.TODO(), pod, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	err = client.waitForContainerRunning(podName, contName, ns, timeout)
	if err != nil {
		return err
	}
	return createAttachFunc(p, command)
}

func (client *K8sClient) removePod(ns string) error {
	return client.Client.CoreV1().Pods(ns).Delete(context.TODO(), podName, metav1.DeleteOptions{})
}
