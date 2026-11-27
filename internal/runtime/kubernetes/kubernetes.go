
package kubernetes

import (
    "context"
    "fmt"
    "io"
    "time"
	"k8s.io/apimachinery/pkg/util/intstr"
    "launchpad/internal/runtime"

    appsv1 "k8s.io/api/apps/v1"
    corev1 "k8s.io/api/core/v1"
    apierrors "k8s.io/apimachinery/pkg/api/errors"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/labels"
    "k8s.io/apimachinery/pkg/util/validation"
    "k8s.io/apimachinery/pkg/util/wait"

    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/tools/clientcmd"
)

type KubernetesRuntime struct {
    client    kubernetes.Interface
    namespace string
}

var _ runtime.Runtime = (*KubernetesRuntime)(nil)

func New(kubeconfig string, contextName string, namespace string,) (*KubernetesRuntime, error) {
	if namespace == "" {
		namespace = "launchpad"
	}

    rules := clientcmd.NewDefaultClientConfigLoadingRules()

	if kubeconfig != "" {
		rules.ExplicitPath = kubeconfig
	}

	overrides := &clientcmd.onfigOverrides{
        CurrentContext: contextName,
    }

	config, err := clientcmd.
        NewNonInteractiveDeferredLoadingClientConfig(
            rules,
            overrides,
        ).ClientConfig()

	if err != nil {
		return nil, fmt.Errorf("load kubernetes config: %w", err)
	}

	client, err := kubernetes.NewForConfig(config)

	if err != nil {
        return nil, fmt.Errorf(
            "create kubernetes client: %w", err,
        )
	}

	return &KubernetesRuntime{
		client: client,
		namespace: namespace,
	}, nil
}

func (r *KubernetesRuntime) Deploy (ctx context.Context, req runtime.DeployRequest) (runtime.Instance, error) {
	if req.ImageRef == "" {
		return runtime.Instance{}, fmt.Errorf("Image reference is required.")
	}

	if req.JobID == "" {
		return runtime.Instance{}, fmt.Errorf("Job ID is required.")
	}

	if req.HostPort != 0 {
		return runtime.Instance{}, fmt.Errorf("Kubernetes does not support host ports")
	}

	if req.ContainerPort < 1 || req.ContainerPort > 65535 {
		return runtime.Instance{}, fmt.Errorf("Container port not valid.")
	}

	name := "lp-" + req.JobID

    if problems := validation.IsDNS1035Label(name); len(problems) > 0 {
        return runtime.Instance{}, fmt.Errorf(
            "invalid kubernetes resource name: %v", problems,
        )
    }

	labels := map[string]string{
		"app":        				name,
		"launchpad.job_id":         req.JobID,
		"launchpad.application_id": req.ApplicationID,
		"launchpad.release_id":     req.ReleaseID,
	}

	selector := map[string]string{
        "app": name,
    }

	replicas := int32(1)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta {
			Name: name,
			namespace: r.namespace,
			Labels: labels,
		},

		Spec: appsv1.DeploymentSpecs{
			Replicas: &replicas,

			Selector: &metav1.LabelSelector{
				MatchLabels: selector,
			},

			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Containers: []corev1.Container{
					{
						Name: "app",
						Image: req.ImageRef,
						ImagePullPolicy: corev1.PullNextPresent,

						Ports: []corev1.ContainerPort {
							{
								ContainerPort: int32(req.ContainerPort),
							},
						},

						ReadinessProbe: &corev1.Probe {
							ProbeHandler: corev1.ProbeHandler {
								TCPSocket: &corev1.TCPSocketAction {
									Port: intstr.FromInt(req.ContainerPort),
								},
							},
							PeriodSeconds: 2,
						},
					},
				},
			},
		},
	}

	created, err := r.client.AppsV1().
		Deployments(r.namespace).Create(ctx, deployment, metav1.CreateOptions{})

	if err != nil {
		return runtime.Instance{}, fmt.Errorf(
            "create kubernetes deployment: %w", err,
        )
	}

	if err := r.createService(ctx, created, req.ContainerPort); err != nil {
		r.cleanupFailedDeployment(name)
		return runtime.Instance{}, err
	}

	if err := r.waitForReady(ctx, name); err != nil {
		r.cleanupFailedDeployment(name)
		return runtime.Instance{}, err
	}

	return runtime.Instance{ID: name}, nil
}