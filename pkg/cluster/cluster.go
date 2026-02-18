package cluster

import (
	"context"
	"strings"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
)

// Variant represents different Kubernetes distributions
type Variant int

const (
	DefaultKubernetes Variant = iota // Vanilla K8s
	OpenShift                        // OpenShift K8s
)

// Distribution represents the K8s distribution for user agent reporting
type Distribution string

const (
	DistributionEKSAddon       Distribution = "eks-addon"
	DistributionEKSSelfManaged Distribution = "eks"
	DistributionROSA           Distribution = "rosa"
	DistributionOther          Distribution = "other"
	DistributionOpenShift      Distribution = "openshift"
)

const (
	kubeSystemNamespace         = "kube-system"
	csiDriverServiceAccountName = "aws-mountpoint-s3-csi-driver"
	eksAddonLabel               = "app.kubernetes.io/managed-by"
	eksAddonAnnotation          = "eks.amazonaws.com/addon"
	rosaLabel                   = "rosa.openshift.io/managed"
)

var defaultMountpointUID = ptr.To(int64(1000))

// DetectVariant determines Kubernetes variant by checking API groups.
func DetectVariant(client *rest.Config, log logr.Logger) Variant {
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(client)
	if err != nil {
		log.Error(err, "Failed to create discovery client")
		return DefaultKubernetes
	}

	apiList, err := discoveryClient.ServerGroups()
	if err != nil {
		log.Error(err, "Failed to get server groups")
		return DefaultKubernetes
	}

	for _, v := range apiList.Groups {
		if v.Name == "config.openshift.io" {
			log.Info("Detected OpenShift variant")
			return OpenShift
		}
	}

	return DefaultKubernetes
}

// DetectDistribution determines the K8s distribution for user agent reporting.
func DetectDistribution(clientset kubernetes.Interface, config *rest.Config, log logr.Logger) Distribution {
	ctx := context.Background()

	variant := DetectVariant(config, log)
	if variant == OpenShift {
		// ROSA is a type of OpenShift, so check for it first
		if isROSA(ctx, clientset, log) {
			return DistributionROSA
		}
		// It's OpenShift but not ROSA (self-hosted or other OpenShift)
		log.Info("Detected OpenShift distribution")
		return DistributionOpenShift
	}

	// Not OpenShift, check for EKS variants
	if isEKSAddon(ctx, clientset, log) {
		return DistributionEKSAddon
	}

	if isEKSCluster(config, log) {
		return DistributionEKSSelfManaged
	}

	log.V(2).Info("Could not detect known distribution, defaulting to other")
	return DistributionOther
}

// isEKSAddon checks if the driver is running as an EKS Addon.
func isEKSAddon(ctx context.Context, clientset kubernetes.Interface, log logr.Logger) bool {
	sa, err := clientset.CoreV1().ServiceAccounts(kubeSystemNamespace).Get(ctx, csiDriverServiceAccountName, metav1.GetOptions{})
	if err != nil {
		log.V(2).Info("Could not find CSI driver service account", "error", err)
		return false
	}

	if sa.Labels[eksAddonLabel] == "eks" {
		log.Info("Detected EKS Addon distribution")
		return true
	}

	if _, ok := sa.Annotations[eksAddonAnnotation]; ok {
		log.Info("Detected EKS Addon distribution")
		return true
	}

	return false
}

// isEKSCluster checks if the cluster is EKS by examining the API server endpoint.
func isEKSCluster(config *rest.Config, log logr.Logger) bool {
	if strings.Contains(config.Host, ".eks.") && strings.Contains(config.Host, ".amazonaws.com") {
		log.Info("Detected EKS cluster distribution")
		return true
	}
	return false
}

// isROSA checks if we're running on ROSA (Red Hat OpenShift Service on AWS).
func isROSA(ctx context.Context, clientset kubernetes.Interface, log logr.Logger) bool {
	ns, err := clientset.CoreV1().Namespaces().Get(ctx, kubeSystemNamespace, metav1.GetOptions{})
	if err != nil {
		log.V(2).Info("Could not get kube-system namespace", "error", err)
		return false
	}

	if ns.Labels[rosaLabel] == "true" {
		log.Info("Detected ROSA distribution")
		return true
	}

	return false
}

// MountpointPodUserID returns the appropriate RunAsUser for Mountpoint Pod based on the cluster variant.
func (c Variant) MountpointPodUserID() *int64 {
	if c == OpenShift {
		// OpenShift clusters automatically assign non-root uid from predefined namespace range
		// https://www.redhat.com/en/blog/a-guide-to-openshift-and-uids
		return nil
	}

	return defaultMountpointUID
}
