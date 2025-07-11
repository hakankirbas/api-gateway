#!/bin/bash

# =============================================================================
# Kubernetes Deployment Script for API Gateway
# =============================================================================

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Default values
ENVIRONMENT="development"
NAMESPACE="api-gateway"
IMAGE_TAG="latest"
KUBECONFIG_PATH=""
DRY_RUN=false
WAIT_TIMEOUT="300s"
FORCE_REBUILD=false

# Function to print colored output
print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to show usage
usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Deploy API Gateway to Kubernetes

OPTIONS:
    -e, --env ENVIRONMENT       Environment (development, staging, production) [default: development]
    -n, --namespace NAMESPACE   Kubernetes namespace [default: api-gateway]
    -t, --tag TAG              Image tag [default: latest]
    -k, --kubeconfig PATH      Path to kubeconfig file
    --dry-run                  Show what would be deployed without applying
    --wait TIMEOUT             Wait timeout for deployment [default: 300s]
    --force-rebuild            Force rebuild Docker image before deploy
    --uninstall                Remove all resources
    -h, --help                 Show this help message

Examples:
    $0 --env development
    $0 --env production --tag v1.0.0
    $0 --dry-run --env staging
    $0 --uninstall --env development
EOF
}

# Function to check dependencies
check_dependencies() {
    local deps=("kubectl" "kustomize" "docker")
    
    for dep in "${deps[@]}"; do
        if ! command -v "$dep" &> /dev/null; then
            print_error "$dep is required but not installed"
            exit 1
        fi
    done
    
    # Check kubectl connectivity
    if ! kubectl cluster-info &> /dev/null; then
        print_error "kubectl cannot connect to cluster"
        print_info "Please check your kubeconfig and cluster connection"
        exit 1
    fi
    
    print_info "All dependencies are available"
}

# Function to build and push Docker image
build_and_push_image() {
    local image_name="api-gateway"
    local full_image_name="${image_name}:${IMAGE_TAG}"
    
    if [[ "$FORCE_REBUILD" == "true" ]] || ! docker image inspect "$full_image_name" &> /dev/null; then
        print_info "Building Docker image: $full_image_name"
        
        # Build image with build script
        if ! ./scripts/build.sh --name "$image_name" --tag "$IMAGE_TAG"; then
            print_error "Failed to build Docker image"
            exit 1
        fi
        
        print_success "Docker image built successfully"
        
        # If using remote registry, push the image
        # Uncomment and modify for your registry
        # print_info "Pushing image to registry..."
        # docker push "$full_image_name"
    else
        print_info "Using existing Docker image: $full_image_name"
    fi
}

# Function to create namespace if it doesn't exist
ensure_namespace() {
    if ! kubectl get namespace "$NAMESPACE" &> /dev/null; then
        print_info "Creating namespace: $NAMESPACE"
        kubectl create namespace "$NAMESPACE"
    else
        print_info "Namespace $NAMESPACE already exists"
    fi
}

# Function to deploy using kustomize
deploy_application() {
    local overlay_path="k8s/overlays/$ENVIRONMENT"
    
    if [[ ! -d "$overlay_path" ]]; then
        print_error "Environment overlay not found: $overlay_path"
        exit 1
    fi
    
    print_info "Deploying to environment: $ENVIRONMENT"
    print_info "Using overlay: $overlay_path"
    
    # Update image tag in kustomization
    cd "$overlay_path"
    kustomize edit set image "api-gateway:$IMAGE_TAG"
    cd - > /dev/null
    
    if [[ "$DRY_RUN" == "true" ]]; then
        print_info "Dry run - showing what would be applied:"
        kustomize build "$overlay_path"
        return
    fi
    
    # Apply the configuration
    print_info "Applying Kubernetes manifests..."
    if kustomize build "$overlay_path" | kubectl apply -f -; then
        print_success "Manifests applied successfully"
    else
        print_error "Failed to apply manifests"
        exit 1
    fi
}

# Function to wait for deployment to be ready
wait_for_deployment() {
    if [[ "$DRY_RUN" == "true" ]]; then
        return
    fi
    
    print_info "Waiting for deployment to be ready..."
    
    local deployments=("api-gateway" "user-service" "product-service")
    
    for deployment in "${deployments[@]}"; do
        print_info "Waiting for deployment/$deployment to be ready..."
        if kubectl wait --for=condition=available deployment/"$deployment" \
           --namespace="$NAMESPACE" --timeout="$WAIT_TIMEOUT"; then
            print_success "Deployment $deployment is ready"
        else
            print_warning "Deployment $deployment is not ready within timeout"
        fi
    done
}

# Function to show deployment status
show_status() {
    if [[ "$DRY_RUN" == "true" ]]; then
        return
    fi
    
    print_info "Deployment Status:"
    echo
    
    # Show deployments
    print_info "Deployments:"
    kubectl get deployments -n "$NAMESPACE" -o wide
    echo
    
    # Show services
    print_info "Services:"
    kubectl get services -n "$NAMESPACE" -o wide
    echo
    
    # Show pods
    print_info "Pods:"
    kubectl get pods -n "$NAMESPACE" -o wide
    echo
    
    # Show access information
    local node_port=$(kubectl get service api-gateway -n "$NAMESPACE" -o jsonpath='{.spec.ports[0].nodePort}' 2>/dev/null || echo "N/A")
    if [[ "$node_port" != "N/A" ]]; then
        print_success "API Gateway is accessible at:"
        print_info "  NodePort: http://localhost:$node_port"
        print_info "  Health Check: http://localhost:$node_port/health"
        print_info "  Metrics: http://localhost:$node_port/metrics"
    fi
}

# Function to uninstall application
uninstall_application() {
    local overlay_path="k8s/overlays/$ENVIRONMENT"
    
    print_warning "Uninstalling API Gateway from environment: $ENVIRONMENT"
    
    if [[ "$DRY_RUN" == "true" ]]; then
        print_info "Dry run - showing what would be deleted:"
        kustomize build "$overlay_path" | kubectl delete --dry-run=client -f -
        return
    fi
    
    read -p "Are you sure you want to delete all resources? (y/N): " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        print_info "Aborted"
        exit 0
    fi
    
    if kustomize build "$overlay_path" | kubectl delete -f -; then
        print_success "Resources deleted successfully"
    else
        print_warning "Some resources may not have been deleted"
    fi
}

# Function to show logs
show_logs() {
    if [[ "$DRY_RUN" == "true" ]]; then
        return
    fi
    
    print_info "Recent logs from API Gateway:"
    kubectl logs -n "$NAMESPACE" -l app.kubernetes.io/name=api-gateway --tail=50
}

# Parse command line arguments
UNINSTALL=false

while [[ $# -gt 0 ]]; do
    case $1 in
        -e|--env)
            ENVIRONMENT="$2"
            shift 2
            ;;
        -n|--namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        -t|--tag)
            IMAGE_TAG="$2"
            shift 2
            ;;
        -k|--kubeconfig)
            KUBECONFIG_PATH="$2"
            export KUBECONFIG="$KUBECONFIG_PATH"
            shift 2
            ;;
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        --wait)
            WAIT_TIMEOUT="$2"
            shift 2
            ;;
        --force-rebuild)
            FORCE_REBUILD=true
            shift
            ;;
        --uninstall)
            UNINSTALL=true
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            print_error "Unknown option: $1"
            usage
            exit 1
            ;;
    esac
done

# Main execution
main() {
    print_info "API Gateway Kubernetes Deployment"
    print_info "Environment: $ENVIRONMENT"
    print_info "Namespace: $NAMESPACE"
    print_info "Image Tag: $IMAGE_TAG"
    
    if [[ "$DRY_RUN" == "true" ]]; then
        print_warning "DRY RUN MODE - No changes will be applied"
    fi
    
    echo
    
    check_dependencies
    
    if [[ "$UNINSTALL" == "true" ]]; then
        uninstall_application
        exit 0
    fi
    
    build_and_push_image
    ensure_namespace
    deploy_application
    wait_for_deployment
    show_status
    
    print_success "Deployment completed successfully!"
    
    if [[ "$ENVIRONMENT" == "development" ]]; then
        echo
        print_info "To view logs: kubectl logs -n $NAMESPACE -l app.kubernetes.io/name=api-gateway -f"
        print_info "To access shell: kubectl exec -n $NAMESPACE -it deployment/api-gateway -- /bin/sh"
    fi
}

# Run main function
main "$@"