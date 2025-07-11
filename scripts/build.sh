#!/bin/bash

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values
IMAGE_NAME="api-gateway"
IMAGE_TAG="latest"
DOCKERFILE="Dockerfile"
BUILD_CONTEXT="."
PLATFORM="linux/amd64"
CACHE_FROM=""
PUSH=false
SCAN=false

# Build metadata
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
COMMIT_SHA=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

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

OPTIONS:
    -n, --name NAME         Image name (default: api-gateway)
    -t, --tag TAG          Image tag (default: latest)
    -f, --file FILE        Dockerfile path (default: Dockerfile)
    -p, --platform PLATFORM Platform (default: linux/amd64)
    --push                 Push image after build
    --scan                 Scan image for vulnerabilities
    --cache-from IMAGE     Use image as cache source
    --no-cache            Don't use build cache
    -h, --help            Show this help message

Examples:
    $0 --name myapp --tag v1.0.0
    $0 --push --scan
    $0 --cache-from myregistry/api-gateway:cache
EOF
}

# Function to check dependencies
check_dependencies() {
    local deps=("docker" "git")
    
    for dep in "${deps[@]}"; do
        if ! command -v "$dep" &> /dev/null; then
            print_error "$dep is required but not installed"
            exit 1
        fi
    done
    
    # Check Docker BuildKit
    if [[ "${DOCKER_BUILDKIT:-}" != "1" ]]; then
        print_warning "DOCKER_BUILDKIT is not enabled. Enabling for this build..."
        export DOCKER_BUILDKIT=1
    fi
}

# Function to build image
build_image() {
    local full_image_name="${IMAGE_NAME}:${IMAGE_TAG}"
    
    print_info "Building Docker image: $full_image_name"
    print_info "Version: $VERSION"
    print_info "Build Time: $BUILD_TIME"
    print_info "Commit SHA: $COMMIT_SHA"
    print_info "Platform: $PLATFORM"
    
    # Build arguments
    local build_args=(
        "--build-arg" "VERSION=$VERSION"
        "--build-arg" "BUILD_TIME=$BUILD_TIME"
        "--build-arg" "COMMIT_SHA=$COMMIT_SHA"
        "--platform" "$PLATFORM"
        "--tag" "$full_image_name"
        "--file" "$DOCKERFILE"
    )
    
    # Add cache options
    if [[ -n "$CACHE_FROM" ]]; then
        build_args+=("--cache-from" "$CACHE_FROM")
    fi
    
    if [[ "${NO_CACHE:-false}" == "true" ]]; then
        build_args+=("--no-cache")
    fi
    
    # Add labels for metadata
    build_args+=(
        "--label" "org.opencontainers.image.version=$VERSION"
        "--label" "org.opencontainers.image.created=$BUILD_TIME"
        "--label" "org.opencontainers.image.revision=$COMMIT_SHA"
        "--label" "org.opencontainers.image.source=$(git config --get remote.origin.url 2>/dev/null || echo 'unknown')"
    )
    
    # Execute build
    if docker build "${build_args[@]}" "$BUILD_CONTEXT"; then
        print_success "Image built successfully: $full_image_name"
    else
        print_error "Failed to build image"
        exit 1
    fi
    
    # Show image size
    local image_size=$(docker images --format "table {{.Size}}" "$full_image_name" | tail -n 1)
    print_info "Image size: $image_size"
}

# Function to scan image for vulnerabilities
scan_image() {
    local full_image_name="${IMAGE_NAME}:${IMAGE_TAG}"
    
    print_info "Scanning image for vulnerabilities..."
    
    if command -v trivy &> /dev/null; then
        trivy image --severity HIGH,CRITICAL "$full_image_name"
    elif command -v docker &> /dev/null && docker run --rm -v /var/run/docker.sock:/var/run/docker.sock aquasec/trivy image --severity HIGH,CRITICAL "$full_image_name" 2>/dev/null; then
        print_success "Vulnerability scan completed"
    else
        print_warning "Trivy not available. Skipping vulnerability scan."
        print_info "Install Trivy for security scanning: https://aquasecurity.github.io/trivy/"
    fi
}

# Function to push image
push_image() {
    local full_image_name="${IMAGE_NAME}:${IMAGE_TAG}"
    
    print_info "Pushing image: $full_image_name"
    
    if docker push "$full_image_name"; then
        print_success "Image pushed successfully"
    else
        print_error "Failed to push image"
        exit 1
    fi
}

# Function to cleanup
cleanup() {
    print_info "Cleaning up dangling images..."
    docker image prune -f || true
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -n|--name)
            IMAGE_NAME="$2"
            shift 2
            ;;
        -t|--tag)
            IMAGE_TAG="$2"
            shift 2
            ;;
        -f|--file)
            DOCKERFILE="$2"
            shift 2
            ;;
        -p|--platform)
            PLATFORM="$2"
            shift 2
            ;;
        --push)
            PUSH=true
            shift
            ;;
        --scan)
            SCAN=true
            shift
            ;;
        --cache-from)
            CACHE_FROM="$2"
            shift 2
            ;;
        --no-cache)
            NO_CACHE=true
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
    print_info "Starting optimized build process..."
    
    check_dependencies
    build_image
    
    if [[ "$SCAN" == "true" ]]; then
        scan_image
    fi
    
    if [[ "$PUSH" == "true" ]]; then
        push_image
    fi
    
    cleanup
    
    print_success "Build process completed!"
    print_info "Image: ${IMAGE_NAME}:${IMAGE_TAG}"
    print_info "To run: docker run -p 8080:8080 ${IMAGE_NAME}:${IMAGE_TAG}"
}

# Run main function
main "$@"