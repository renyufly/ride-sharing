package kubernetes

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsclient "k8s.io/client-go/kubernetes/typed/apps/v1"
	"k8s.io/client-go/rest"
)

const matcherDeployment = "matcher-api"

type MatcherScaler struct {
	deployments appsclient.DeploymentInterface
}

func NewMatcherScaler(namespace string) (*MatcherScaler, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	cfg.Timeout = 3 * time.Second

	client, err := appsclient.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &MatcherScaler{deployments: client.Deployments(namespace)}, nil
}

func (s *MatcherScaler) Get(ctx context.Context) (desired, ready int32, err error) {
	deployment, err := s.deployments.Get(ctx, matcherDeployment, metav1.GetOptions{})
	if err != nil {
		return 0, 0, err
	}

	desired = 1
	if deployment.Spec.Replicas != nil {
		desired = *deployment.Spec.Replicas
	}
	return desired, deployment.Status.ReadyReplicas, nil
}

func (s *MatcherScaler) Set(ctx context.Context, replicas int32) error {
	scale, err := s.deployments.GetScale(ctx, matcherDeployment, metav1.GetOptions{})
	if err != nil {
		return err
	}
	scale.Spec.Replicas = replicas
	_, err = s.deployments.UpdateScale(ctx, matcherDeployment, scale, metav1.UpdateOptions{})
	return err
}