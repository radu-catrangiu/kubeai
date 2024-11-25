package modelevaluator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/substratusai/kubeai/internal/config"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const deploymentName = "kubeai-llm-size-service"

var serviceHost = ""

func Init(ctx context.Context, k8sClient client.Client, namespace string, cfg config.System) {
	namespacedName := types.NamespacedName{
		Name:      deploymentName,
		Namespace: namespace,
	}
	deployment := &appsv1.Deployment{}
	err := k8sClient.Get(ctx, namespacedName, deployment)
	if err == nil && deployment.Status.AvailableReplicas > 0 {
		fmt.Printf("Deployment %s already exists and is running\n", deploymentName)
	}

	podSpec := corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:  "llm-size-service",
				Image: cfg.ModelEvaluators.Huggingface.Image, // Replace with your desired image
				Ports: []corev1.ContainerPort{
					{
						ContainerPort: 3000,
					},
				},
				Env: []corev1.EnvVar{
					{
						Name: "HF_API_KEY",
						ValueFrom: &corev1.EnvVarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: cfg.SecretNames.Huggingface,
								},
								Key:      "token",
								Optional: ptr.To(true),
							},
						},
					},
					{
						Name:  "SERVER_HOST",
						Value: "0.0.0.0",
					},
				},
			},
		},
	}

	labels := map[string]string{
		"app":                          "llm-size-service",
		"app.kubernetes.io/managed-by": "kubeai",
	}

	deployment = &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: podSpec,
			},
		},
	}

	err = k8sClient.Create(ctx, deployment)
	if err != nil && !errors.IsAlreadyExists(err) {
		fmt.Printf("Failed to create deployment: %v", err)
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Port:       3000,
					TargetPort: intstr.FromInt(3000),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}

	err = k8sClient.Create(ctx, service)
	if err != nil && !errors.IsAlreadyExists(err) {
		fmt.Printf("Failed to create service: %v", err)
	}

	serviceHost = fmt.Sprintf("%s.%s", deploymentName, namespace)
	if len(cfg.ModelEvaluators.Huggingface.DevProxyHost) > 0 {
		serviceHost = cfg.ModelEvaluators.Huggingface.DevProxyHost
	}
}

func Cleanup(ctx context.Context, k8sClient client.Client, namespace string) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespace,
		},
	}
	err := k8sClient.Delete(ctx, deployment)
	if err != nil {
		fmt.Printf("Failed to delete deployment: %v", err)
	}
}

type modelSizeResponse struct {
	Size        string `json:"size"`
	RoundedSize string `json:"roundedSize"`
}

func GetModelSize(model string) (string, error) {
	urlStr := fmt.Sprintf("http://%s:%d/evaluate?model=%s", serviceHost, 3000, model)
	svcUrl, err := url.Parse(urlStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse url: %w", err)
	}

	client := &http.Client{}
	req, err := http.NewRequest("GET", svcUrl.String(), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	var resp modelSizeResponse

	err = json.Unmarshal(body, &resp)
	if err != nil {
		return "", fmt.Errorf("failed to parse response body: %w", err)
	}

	return resp.RoundedSize, nil
}
