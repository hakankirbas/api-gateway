#!/bin/bash

# =============================================================================
# Test Service Discovery Integration
# =============================================================================

set -euo pipefail

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

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

# Check if kubectl is available
if ! command -v kubectl &> /dev/null; then
    print_error "kubectl is required but not installed"
    exit 1
fi

print_info "Testing Service Discovery Integration..."

# Step 1: Add discovery annotations to existing services
print_info "Adding service discovery annotations..."

# Annotate user-service
kubectl annotate service user-service -n api-gateway \
    gateway.io/enabled=true \
    gateway.io/path=/users \
    gateway.io/method=GET \
    gateway.io/auth-required=true \
    gateway.io/load-balancing=round-robin \
    --overwrite

# Annotate product-service  
kubectl annotate service product-service -n api-gateway \
    gateway.io/enabled=true \
    gateway.io/path=/products \
    gateway.io/method=GET \
    gateway.io/auth-required=false \
    gateway.io/load-balancing=round-robin \
    --overwrite

print_success "Service annotations added"

# Step 2: Check annotations
print_info "Verifying service annotations..."
echo "User Service annotations:"
kubectl get service user-service -n api-gateway -o jsonpath='{.metadata.annotations}' | jq .
echo
echo "Product Service annotations:"
kubectl get service product-service -n api-gateway -o jsonpath='{.metadata.annotations}' | jq .
echo

# Step 3: Check service endpoints
print_info "Checking service endpoints..."
kubectl get endpoints -n api-gateway

# Step 4: Build and deploy updated gateway
print_info "Building updated API Gateway with service discovery..."
if make build-docker; then
    print_success "API Gateway built successfully"
else
    print_error "Failed to build API Gateway"
    exit 1
fi

# Step 5: Update deployment
print_info "Updating deployment..."
kubectl rollout restart deployment/api-gateway -n api-gateway
kubectl rollout status deployment/api-gateway -n api-gateway

# Step 6: Wait for pods to be ready
print_info "Waiting for gateway to be ready..."
sleep 30

# Step 7: Test the endpoints
print_info "Testing service discovery..."

GATEWAY_URL="http://localhost:30080"

# Test health endpoint
print_info "Testing health endpoint..."
if curl -f -s $GATEWAY_URL/health > /dev/null; then
    print_success "Health endpoint working"
else
    print_error "Health endpoint failed"
fi

# Test products endpoint
print_info "Testing products endpoint..."
if curl -f -s $GATEWAY_URL/products > /dev/null; then
    print_success "Products endpoint working"
else
    print_error "Products endpoint failed"
fi

# Test users endpoint (should require auth)
print_info "Testing users endpoint without auth..."
if curl -f -s $GATEWAY_URL/users > /dev/null; then
    print_warning "Users endpoint accessible without auth (unexpected)"
else
    print_success "Users endpoint properly protected"
fi

# Get JWT token and test with auth
print_info "Testing users endpoint with auth..."
TOKEN=$(curl -s -X POST $GATEWAY_URL/login \
    -H "Content-Type: application/json" \
    -d '{"username": "Hako", "password": "123"}')

if [[ -n "$TOKEN" ]]; then
    if curl -f -s -H "Authorization: Bearer $TOKEN" $GATEWAY_URL/users > /dev/null; then
        print_success "Users endpoint working with auth"
    else
        print_error "Users endpoint failed with auth"
    fi
else
    print_error "Failed to get JWT token"
fi

# Step 8: Check gateway logs for service discovery
print_info "Checking gateway logs for service discovery..."
kubectl logs -n api-gateway deployment/api-gateway --tail=20 | grep -i "discovery\|service\|route" || true

print_success "Service discovery test completed!"
print_info "Check the gateway logs for detailed service discovery activity"