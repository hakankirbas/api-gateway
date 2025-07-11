package k8s

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// Client wraps the Kubernetes client with additional functionality
type Client struct {
	Clientset kubernetes.Interface
	Config    *rest.Config
	Namespace string
}

// ClientConfig holds configuration for the Kubernetes client
type ClientConfig struct {
	KubeConfig string
	Namespace  string
	InCluster  bool
}

// NewClient creates a new Kubernetes client
func NewClient(config ClientConfig) (*Client, error) {
	var restConfig *rest.Config
	var err error

	if config.InCluster {
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to create in-cluster config: %w", err)
		}
		log.Println("Using in-cluster Kubernetes configuration")
	} else {
		kubeconfigPath := config.KubeConfig
		if kubeconfigPath == "" {
			if home := homedir.HomeDir(); home != "" {
				kubeconfigPath = filepath.Join(home, ".kube", "config")
			}
		}

		restConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create kubeconfig from %s: %w", kubeconfigPath, err)
		}
		log.Printf("Using kubeconfig from: %s", kubeconfigPath)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes clientset: %w", err)
	}

	namespace := config.Namespace
	if namespace == "" {
		namespace = "default"
		if ns := os.Getenv("KUBERNETES_NAMESPACE"); ns != "" {
			namespace = ns
		}
	}

	client := &Client{
		Clientset: clientset,
		Config:    restConfig,
		Namespace: namespace,
	}

	if err := client.TestConnection(); err != nil {
		return nil, fmt.Errorf("failed to connect to Kubernetes cluster: %w", err)
	}

	log.Printf("Successfully connected to Kubernetes cluster, using namespace: %s", namespace)
	return client, nil
}

// TestConnection verifies that the client can connect to the Kubernetes API
func (c *Client) TestConnection() error {
	_, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	version, err := c.Clientset.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("failed to get server version: %w", err)
	}

	log.Printf("Connected to Kubernetes server version: %s", version.String())
	return nil
}

// GetNamespace returns the default namespace for this client
func (c *Client) GetNamespace() string {
	return c.Namespace
}

// IsInCluster checks if we're running inside a Kubernetes cluster
func IsInCluster() bool {
	_, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/token")
	return err == nil
}

// AutoDetectConfig automatically detects whether to use in-cluster or external config
func AutoDetectConfig(namespace string) ClientConfig {
	return ClientConfig{
		InCluster: IsInCluster(),
		Namespace: namespace,
	}
}
