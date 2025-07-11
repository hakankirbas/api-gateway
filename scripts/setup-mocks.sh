#!/bin/bash

# =============================================================================
# Script to create mock service files for development
# =============================================================================

set -euo pipefail

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

# Create directory structure
print_info "Creating mock service directories..."

# Create directories
mkdir -p mocks/user-service
mkdir -p mocks/product-service
mkdir -p monitoring

# Create user service mock responses
print_info "Creating user service mock..."

cat > mocks/user-service/index.html << 'EOF'
{
  "service": "user-service",
  "version": "1.0.0",
  "status": "running"
}
EOF

cat > mocks/user-service/health << 'EOF'
{
  "status": "healthy",
  "timestamp": "2024-01-01T00:00:00Z",
  "service": "user-service"
}
EOF

cat > mocks/user-service/users << 'EOF'
{
  "users": [
    {
      "id": 1,
      "username": "john_doe",
      "email": "john@example.com",
      "created_at": "2024-01-01T00:00:00Z"
    },
    {
      "id": 2,
      "username": "jane_smith",
      "email": "jane@example.com",
      "created_at": "2024-01-01T00:00:00Z"
    }
  ]
}
EOF

# Create product service mock responses
print_info "Creating product service mock..."

cat > mocks/product-service/index.html << 'EOF'
{
  "service": "product-service",
  "version": "1.0.0",
  "status": "running"
}
EOF

cat > mocks/product-service/health << 'EOF'
{
  "status": "healthy",
  "timestamp": "2024-01-01T00:00:00Z",
  "service": "product-service"
}
EOF

cat > mocks/product-service/products << 'EOF'
{
  "products": [
    {
      "id": 1,
      "name": "Laptop",
      "price": 999.99,
      "category": "Electronics",
      "in_stock": true
    },
    {
      "id": 2,
      "name": "Coffee Mug",
      "price": 12.99,
      "category": "Kitchen",
      "in_stock": true
    }
  ]
}
EOF

print_success "Mock services created successfully!"
print_info "You can now run: make dev"