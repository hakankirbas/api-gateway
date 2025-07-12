# Variables
APP_NAME := api-gateway
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
COMMIT_SHA := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Docker settings
DOCKER_REGISTRY ?= 
IMAGE_NAME := $(DOCKER_REGISTRY)$(APP_NAME)
IMAGE_TAG ?= latest
DOCKERFILE := Dockerfile
PLATFORM := linux/amd64

# Kubernetes settings
K8S_NAMESPACE ?= api-gateway
K8S_ENVIRONMENT ?= development
K8S_IMAGE_TAG ?= latest

# Go settings
GO_VERSION := 1.21
BINARY_NAME := gateway
GO_PACKAGES := ./...

# Directories
BUILD_DIR := build
DIST_DIR := dist
COVERAGE_DIR := coverage

# Colors
RED := \033[0;31m
GREEN := \033[0;32m
YELLOW := \033[1;33m
BLUE := \033[0;34m
NC := \033[0m

# Default target
.DEFAULT_GOAL := help

# =============================================================================
# Help
# =============================================================================

.PHONY: help
help: ## Show this help message
	@echo "$(BLUE)API Gateway Makefile$(NC)"
	@echo "$(YELLOW)Available targets:$(NC)"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  $(GREEN)%-20s$(NC) %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# =============================================================================
# Development
# =============================================================================

.PHONY: dev
dev: setup-mocks ## Run development environment with Docker Compose
	@echo "$(BLUE)Starting development environment...$(NC)"
	docker-compose up --build

.PHONY: dev-full
dev-full: setup-mocks ## Run development environment with all services (Redis, Prometheus)
	@echo "$(BLUE)Starting full development environment...$(NC)"
	docker-compose --profile caching --profile monitoring up --build

.PHONY: dev-down
dev-down: ## Stop development environment
	@echo "$(BLUE)Stopping development environment...$(NC)"
	docker-compose down

.PHONY: dev-logs
dev-logs: ## Show development logs
	docker-compose logs -f gateway

# =============================================================================
# Build
# =============================================================================

.PHONY: build
build: ## Build Go binary locally
	@echo "$(BLUE)Building $(BINARY_NAME)...$(NC)"
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-ldflags="-w -s -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME) -X main.commitSHA=$(COMMIT_SHA)" \
		-o $(BUILD_DIR)/$(BINARY_NAME) \
		./cmd/gateway

.PHONY: build-docker
build-docker: ## Build Docker image
	@echo "$(BLUE)Building Docker image: $(IMAGE_NAME):$(IMAGE_TAG)$(NC)"
	./scripts/build.sh --name $(IMAGE_NAME) --tag $(IMAGE_TAG)

.PHONY: build-docker-dev
build-docker-dev: ## Build Docker image for development
	@echo "$(BLUE)Building development Docker image...$(NC)"
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		--build-arg COMMIT_SHA=$(COMMIT_SHA) \
		--tag $(APP_NAME):dev \
		--file $(DOCKERFILE) \
		.

.PHONY: build-multi-arch
build-multi-arch: ## Build multi-architecture Docker image
	@echo "$(BLUE)Building multi-architecture Docker image...$(NC)"
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		--build-arg COMMIT_SHA=$(COMMIT_SHA) \
		--tag $(IMAGE_NAME):$(IMAGE_TAG) \
		--file $(DOCKERFILE) \
		--push \
		.

# =============================================================================
# Testing
# =============================================================================

.PHONY: test
test: ## Run tests
	@echo "$(BLUE)Running tests...$(NC)"
	go test -v $(GO_PACKAGES)

.PHONY: test-coverage
test-coverage: ## Run tests with coverage
	@echo "$(BLUE)Running tests with coverage...$(NC)"
	@mkdir -p $(COVERAGE_DIR)
	go test -v -race -coverprofile=$(COVERAGE_DIR)/coverage.out $(GO_PACKAGES)
	go tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/coverage.html
	@echo "$(GREEN)Coverage report: $(COVERAGE_DIR)/coverage.html$(NC)"

.PHONY: test-integration
test-integration: ## Run integration tests
	@echo "$(BLUE)Running integration tests...$(NC)"
	go test -v -tags=integration $(GO_PACKAGES)

.PHONY: benchmark
benchmark: ## Run benchmarks
	@echo "$(BLUE)Running benchmarks...$(NC)"
	go test -bench=. -benchmem $(GO_PACKAGES)

# =============================================================================
# Code Quality
# =============================================================================

.PHONY: lint
lint: ## Run linters
	@echo "$(BLUE)Running linters...$(NC)"
	golangci-lint run

.PHONY: format
format: ## Format code
	@echo "$(BLUE)Formatting code...$(NC)"
	go fmt $(GO_PACKAGES)
	goimports -w .

.PHONY: vet
vet: ## Run go vet
	@echo "$(BLUE)Running go vet...$(NC)"
	go vet $(GO_PACKAGES)

.PHONY: security
security: ## Run security checks
	@echo "$(BLUE)Running security checks...$(NC)"
	gosec $(GO_PACKAGES)

# =============================================================================
# Dependencies
# =============================================================================

.PHONY: deps
deps: ## Download dependencies
	@echo "$(BLUE)Downloading dependencies...$(NC)"
	go mod download

.PHONY: deps-update
deps-update: ## Update dependencies
	@echo "$(BLUE)Updating dependencies...$(NC)"
	go get -u ./...
	go mod tidy

.PHONY: deps-verify
deps-verify: ## Verify dependencies
	@echo "$(BLUE)Verifying dependencies...$(NC)"
	go mod verify

# =============================================================================
# Docker Operations
# =============================================================================

.PHONY: docker-run
docker-run: ## Run Docker container
	@echo "$(BLUE)Running Docker container...$(NC)"
	docker run -d \
		--name $(APP_NAME)-container \
		-p 8080:8080 \
		$(IMAGE_NAME):$(IMAGE_TAG)

.PHONY: docker-stop
docker-stop: ## Stop Docker container
	@echo "$(BLUE)Stopping Docker container...$(NC)"
	docker stop $(APP_NAME)-container || true
	docker rm $(APP_NAME)-container || true

.PHONY: docker-scan
docker-scan: ## Scan Docker image for vulnerabilities
	@echo "$(BLUE)Scanning Docker image...$(NC)"
	./scripts/build.sh --name $(IMAGE_NAME) --tag $(IMAGE_TAG) --scan

.PHONY: docker-push
docker-push: ## Push Docker image
	@echo "$(BLUE)Pushing Docker image...$(NC)"
	docker push $(IMAGE_NAME):$(IMAGE_TAG)

# =============================================================================
# Deployment
# =============================================================================

.PHONY: deploy-local
deploy-local: build-docker docker-run ## Deploy locally
	@echo "$(GREEN)Deployed locally on http://localhost:8080$(NC)"

.PHONY: deploy-staging
deploy-staging: ## Deploy to staging
	@echo "$(BLUE)Deploying to staging...$(NC)"
	# Add your staging deployment commands here

.PHONY: deploy-prod
deploy-prod: ## Deploy to production
	@echo "$(BLUE)Deploying to production...$(NC)"
	# Add your production deployment commands here

# =============================================================================
# Utilities
# =============================================================================

.PHONY: clean
clean: ## Clean build artifacts
	@echo "$(BLUE)Cleaning build artifacts...$(NC)"
	rm -rf $(BUILD_DIR)
	rm -rf $(DIST_DIR)
	rm -rf $(COVERAGE_DIR)
	docker image prune -f

.PHONY: clean-all
clean-all: clean ## Clean everything including Docker images
	@echo "$(BLUE)Cleaning everything...$(NC)"
	docker system prune -af
	docker volume prune -f

.PHONY: logs
logs: ## Show application logs
	docker logs -f $(APP_NAME)-container

.PHONY: shell
shell: ## Open shell in running container
	docker exec -it $(APP_NAME)-container /bin/sh

.PHONY: health
health: ## Check application health
	@echo "$(BLUE)Checking application health...$(NC)"
	curl -f http://localhost:8080/health || echo "$(RED)Health check failed$(NC)"

.PHONY: version
version: ## Show version information
	@echo "App Name: $(APP_NAME)"
	@echo "Version: $(VERSION)"
	@echo "Build Time: $(BUILD_TIME)"
	@echo "Commit SHA: $(COMMIT_SHA)"
	@echo "Image: $(IMAGE_NAME):$(IMAGE_TAG)"

# =============================================================================
# Setup
# =============================================================================

.PHONY: setup-mocks
setup-mocks: ## Create mock service files
	@echo "$(BLUE)Setting up mock services...$(NC)"
	@mkdir -p mocks/user-service mocks/product-service monitoring
	@chmod +x scripts/setup-mocks.sh
	@./scripts/setup-mocks.sh

.PHONY: setup
setup: ## Setup development environment
	@echo "$(BLUE)Setting up development environment...$(NC)"
	go mod download
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "$(YELLOW)Installing golangci-lint...$(NC)"; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin v1.54.2; \
	fi
	@echo "$(GREEN)Setup complete!$(NC)"

.PHONY: install-tools
install-tools: ## Install development tools
	@echo "$(BLUE)Installing development tools...$(NC)"
	go install golang.org/x/tools/cmd/goimports@latest
	go install github.com/securecodewarrior/gosec/v2/cmd/gosec@latest
	@echo "$(GREEN)Tools installed!$(NC)"

.PHONY: k8s-check
k8s-check: ## Check Kubernetes dependencies
	@echo "$(BLUE)Checking Kubernetes dependencies...$(NC)"
	@command -v kubectl >/dev/null 2>&1 || { echo "$(RED)kubectl is required$(NC)"; exit 1; }
	@command -v kustomize >/dev/null 2>&1 || { echo "$(RED)kustomize is required$(NC)"; exit 1; }
	@kubectl cluster-info >/dev/null 2>&1 || { echo "$(RED)Cannot connect to Kubernetes cluster$(NC)"; exit 1; }
	@echo "$(GREEN)Kubernetes dependencies OK$(NC)"

.PHONY: k8s-build
k8s-build: build-docker ## Build image for Kubernetes deployment
	@echo "$(BLUE)Building image for Kubernetes...$(NC)"
	docker tag $(APP_NAME):latest $(APP_NAME):$(K8S_IMAGE_TAG)

.PHONY: k8s-deploy-dev
k8s-deploy-dev: k8s-check k8s-build ## Deploy to development environment
	@echo "$(BLUE)Deploying to Kubernetes development environment...$(NC)"
	@chmod +x scripts/deploy-k8s.sh
	./scripts/deploy-k8s.sh --env development --tag $(K8S_IMAGE_TAG)

.PHONY: k8s-deploy-staging
k8s-deploy-staging: k8s-check k8s-build ## Deploy to staging environment
	@echo "$(BLUE)Deploying to Kubernetes staging environment...$(NC)"
	@chmod +x scripts/deploy-k8s.sh
	./scripts/deploy-k8s.sh --env staging --tag $(K8S_IMAGE_TAG)

.PHONY: k8s-deploy-prod
k8s-deploy-prod: k8s-check k8s-build ## Deploy to production environment
	@echo "$(BLUE)Deploying to Kubernetes production environment...$(NC)"
	@chmod +x scripts/deploy-k8s.sh
	./scripts/deploy-k8s.sh --env production --tag $(K8S_IMAGE_TAG)

.PHONY: k8s-dry-run
k8s-dry-run: k8s-check ## Dry run deployment (show what would be deployed)
	@echo "$(BLUE)Performing dry run deployment...$(NC)"
	./scripts/deploy-k8s.sh --env $(K8S_ENVIRONMENT) --dry-run

.PHONY: k8s-status
k8s-status: k8s-check ## Show Kubernetes deployment status
	@echo "$(BLUE)Kubernetes deployment status:$(NC)"
	kubectl get all -n $(K8S_NAMESPACE)

.PHONY: k8s-logs
k8s-logs: k8s-check ## Show API Gateway logs
	@echo "$(BLUE)API Gateway logs:$(NC)"
	kubectl logs -n $(K8S_NAMESPACE) -l app.kubernetes.io/name=api-gateway --tail=100 -f

.PHONY: k8s-shell
k8s-shell: k8s-check ## Open shell in API Gateway pod
	@echo "$(BLUE)Opening shell in API Gateway pod...$(NC)"
	kubectl exec -n $(K8S_NAMESPACE) -it deployment/api-gateway -- /bin/sh

.PHONY: k8s-port-forward
k8s-port-forward: k8s-check ## Port forward to API Gateway
	@echo "$(BLUE)Port forwarding to API Gateway (localhost:8080)...$(NC)"
	kubectl port-forward -n $(K8S_NAMESPACE) service/api-gateway-internal 8080:8080

.PHONY: k8s-restart
k8s-restart: k8s-check ## Restart API Gateway deployment
	@echo "$(BLUE)Restarting API Gateway deployment...$(NC)"
	kubectl rollout restart deployment/api-gateway -n $(K8S_NAMESPACE)
	kubectl rollout status deployment/api-gateway -n $(K8S_NAMESPACE)

.PHONY: k8s-scale
k8s-scale: k8s-check ## Scale API Gateway deployment (use REPLICAS=n)
	@echo "$(BLUE)Scaling API Gateway to $(REPLICAS) replicas...$(NC)"
	kubectl scale deployment/api-gateway -n $(K8S_NAMESPACE) --replicas=$(REPLICAS)

.PHONY: k8s-uninstall
k8s-uninstall: k8s-check ## Uninstall from Kubernetes
	@echo "$(BLUE)Uninstalling from Kubernetes...$(NC)"
	./scripts/deploy-k8s.sh --env $(K8S_ENVIRONMENT) --uninstall

.PHONY: k8s-clean
k8s-clean: k8s-check ## Clean up Kubernetes resources
	@echo "$(BLUE)Cleaning up Kubernetes resources...$(NC)"
	kubectl delete namespace $(K8S_NAMESPACE) --ignore-not-found=true

# =============================================================================
# Kubernetes Testing and Monitoring
# =============================================================================

.PHONY: k8s-test
k8s-test: k8s-check ## Test API Gateway endpoints in Kubernetes
	@echo "$(BLUE)Testing API Gateway endpoints...$(NC)"
	@kubectl get service api-gateway -n $(K8S_NAMESPACE) -o jsonpath='{.spec.ports[0].nodePort}' > /tmp/nodeport
	@NODE_PORT=$$(cat /tmp/nodeport) && \
	echo "Testing health endpoint..." && \
	curl -f http://localhost:$$NODE_PORT/health && \
	echo "Testing metrics endpoint..." && \
	curl -f http://localhost:$$NODE_PORT/metrics && \
	echo "Testing products endpoint..." && \
	curl -f http://localhost:$$NODE_PORT/products && \
	echo "$(GREEN)All tests passed!$(NC)"

.PHONY: k8s-health
k8s-health: k8s-check ## Check health of all services
	@echo "$(BLUE)Checking health of all services...$(NC)"
	kubectl get pods -n $(K8S_NAMESPACE) -o wide
	kubectl get endpoints -n $(K8S_NAMESPACE)

.PHONY: k8s-events
k8s-events: k8s-check ## Show recent events
	@echo "$(BLUE)Recent Kubernetes events:$(NC)"
	kubectl get events -n $(K8S_NAMESPACE) --sort-by='.lastTimestamp'

.PHONY: k8s-describe
k8s-describe: k8s-check ## Describe API Gateway deployment
	@echo "$(BLUE)API Gateway deployment details:$(NC)"
	kubectl describe deployment api-gateway -n $(K8S_NAMESPACE)

# =============================================================================
# Kubernetes Configuration Management
# =============================================================================

.PHONY: k8s-config-update
k8s-config-update: k8s-check ## Update ConfigMap without redeploying
	@echo "$(BLUE)Updating ConfigMap...$(NC)"
	kubectl create configmap api-gateway-config \
		--from-file=configs/gateway.yaml \
		--dry-run=client -o yaml | \
		kubectl apply -n $(K8S_NAMESPACE) -f -

.PHONY: k8s-secret-update
k8s-secret-update: k8s-check ## Update Secret (prompt for JWT secret)
	@echo "$(BLUE)Updating Secret...$(NC)"
	@read -s -p "Enter JWT Secret: " JWT_SECRET && \
	kubectl create secret generic api-gateway-secret \
		--from-literal=JWT_SECRET=$$JWT_SECRET \
		--dry-run=client -o yaml | \
		kubectl apply -n $(K8S_NAMESPACE) -f -

.PHONY: k8s-rollback
k8s-rollback: k8s-check ## Rollback to previous deployment
	@echo "$(BLUE)Rolling back to previous deployment...$(NC)"
	kubectl rollout undo deployment/api-gateway -n $(K8S_NAMESPACE)
	kubectl rollout status deployment/api-gateway -n $(K8S_NAMESPACE)

# =============================================================================
# Development Helpers
# =============================================================================

.PHONY: k8s-dev-setup
k8s-dev-setup: ## Setup local Kubernetes development environment
	@echo "$(BLUE)Setting up Kubernetes development environment...$(NC)"
	@if command -v kind >/dev/null 2>&1; then \
		echo "Creating kind cluster..."; \
		kind create cluster --name api-gateway || echo "Cluster may already exist"; \
	elif command -v minikube >/dev/null 2>&1; then \
		echo "Starting minikube..."; \
		minikube start || echo "Minikube may already be running"; \
	else \
		echo "$(YELLOW)Please install kind or minikube for local development$(NC)"; \
	fi

.PHONY: k8s-dev-teardown
k8s-dev-teardown: ## Teardown local Kubernetes development environment
	@echo "$(BLUE)Tearing down Kubernetes development environment...$(NC)"
	@if command -v kind >/dev/null 2>&1; then \
		kind delete cluster --name api-gateway; \
	elif command -v minikube >/dev/null 2>&1; then \
		minikube stop; \
		minikube delete; \
	fi

# Help text additions
.PHONY: k8s-help
k8s-help: ## Show Kubernetes-specific help
	@echo "$(BLUE)Kubernetes Commands:$(NC)"
	@echo "  $(GREEN)k8s-deploy-dev$(NC)     - Deploy to development"
	@echo "  $(GREEN)k8s-deploy-staging$(NC) - Deploy to staging"
	@echo "  $(GREEN)k8s-deploy-prod$(NC)    - Deploy to production"
	@echo "  $(GREEN)k8s-status$(NC)         - Show deployment status"
	@echo "  $(GREEN)k8s-logs$(NC)           - Show application logs"
	@echo "  $(GREEN)k8s-test$(NC)           - Test endpoints"
	@echo "  $(GREEN)k8s-uninstall$(NC)      - Remove deployment"

# =============================================================================
# Enhanced Testing and Monitoring
# =============================================================================

.PHONY: test-enhanced
test-enhanced: ## Test enhanced load balancing and circuit breaking features
	@echo "$(BLUE)Testing enhanced API Gateway features...$(NC)"
	@chmod +x scripts/test-enhanced.sh
	@./scripts/test-enhanced.sh

.PHONY: test-load-balancing
test-load-balancing: ## Test load balancing functionality specifically
	@echo "$(BLUE)Testing load balancing...$(NC)"
	@echo "Getting JWT token..."
	@TOKEN=$(curl -s -X POST http://localhost:30080/login \
		-H "Content-Type: application/json" \
		-d '{"username": "Hako", "password": "123"}' | tr -d '"'); \
	echo "Testing products endpoint (10 requests):"; \
	for i in $(seq 1 10); do \
		curl -s http://localhost:30080/products | jq -r '.service // "N/A"' | sed "s/^/Request $i: /"; \
		sleep 0.5; \
	done; \
	echo "Testing users endpoint (10 requests):"; \
	for i in $(seq 1 10); do \
		curl -s -H "Authorization: Bearer $TOKEN" http://localhost:30080/users | jq -r '.service // "N/A"' | sed "s/^/Request $i: /"; \
		sleep 0.5; \
	done

.PHONY: test-circuit-breaker
test-circuit-breaker: ## Test circuit breaker functionality
	@echo "$(BLUE)Testing circuit breaker...$(NC)"
	@echo "Scaling down user-service to trigger circuit breaker..."
	@kubectl scale deployment user-service -n api-gateway --replicas=0
	@sleep 10
	@echo "Making requests to trigger circuit breaker..."
	@TOKEN=$(curl -s -X POST http://localhost:30080/login \
		-H "Content-Type: application/json" \
		-d '{"username": "Hako", "password": "123"}' | tr -d '"'); \
	for i in $(seq 1 15); do \
		response=$(curl -s -w "%{http_code}" -H "Authorization: Bearer $TOKEN" http://localhost:30080/users -o /dev/null); \
		echo "Request $i: HTTP $response"; \
		sleep 1; \
	done
	@echo "Circuit breaker status:"
	@curl -s http://localhost:30080/admin/circuit-breakers | jq .
	@echo "Restoring user-service..."
	@kubectl scale deployment user-service -n api-gateway --replicas=1

.PHONY: monitor-admin
monitor-admin: ## Show admin endpoints status
	@echo "$(BLUE)Admin endpoints status:$(NC)"
	@echo "Services:"
	@curl -s http://localhost:30080/admin/services | jq .
	@echo -e "\nRoutes:"
	@curl -s http://localhost:30080/admin/routes | jq .
	@echo -e "\nLoad Balancers:"
	@curl -s http://localhost:30080/admin/load-balancers | jq .
	@echo -e "\nCircuit Breakers:"
	@curl -s http://localhost:30080/admin/circuit-breakers | jq .
	@echo -e "\nHealth Overview:"
	@curl -s http://localhost:30080/admin/health-overview | jq .

.PHONY: stress-test
stress-test: ## Run stress test to validate load balancing and circuit breaking
	@echo "$(BLUE)Running stress test...$(NC)"
	@echo "Getting JWT token..."
	@TOKEN=$(curl -s -X POST http://localhost:30080/login \
		-H "Content-Type: application/json" \
		-d '{"username": "Hako", "password": "123"}' | tr -d '"'); \
	echo "Running 100 concurrent requests..."; \
	seq 1 100 | xargs -n1 -P10 -I{} sh -c "curl -s -H \"Authorization: Bearer $TOKEN\" http://localhost:30080/users > /dev/null; curl -s http://localhost:30080/products > /dev/null; echo \"Batch {} completed\""
	@echo "Stress test completed. Checking statistics..."
	@curl -s http://localhost:30080/admin/health-overview | jq .

.PHONY: watch-logs
watch-logs: ## Watch API Gateway logs with enhanced filtering
	@echo "$(BLUE)Watching API Gateway logs...$(NC)"
	kubectl logs -n api-gateway deployment/api-gateway -f | grep -E "(load.balanc|circuit.break|endpoint|route|ERROR|WARN)" --color=always

.PHONY: deploy-enhanced
deploy-enhanced: build-docker k8s-deploy-dev test-enhanced ## Build, deploy and test enhanced features
	@echo "$(GREEN)Enhanced API Gateway deployed and tested successfully!$(NC)"

.PHONY: benchmark-enhanced
benchmark-enhanced: ## Benchmark the enhanced gateway
	@echo "$(BLUE)Benchmarking enhanced API Gateway...$(NC)"
	@command -v ab >/dev/null 2>&1 || { echo "$(RED)Apache Bench (ab) is required$(NC)"; exit 1; }
	@TOKEN=$(curl -s -X POST http://localhost:30080/login \
		-H "Content-Type: application/json" \
		-d '{"username": "Hako", "password": "123"}' | tr -d '"'); \
	echo "Benchmarking products endpoint (no auth):"; \
	ab -n 1000 -c 10 http://localhost:30080/products; \
	echo "Benchmarking users endpoint (with auth):"; \
	ab -n 1000 -c 10 -H "Authorization: Bearer $TOKEN" http://localhost:30080/users
	@echo "Post-benchmark statistics:"
	@curl -s http://localhost:30080/admin/health-overview | jq .

.PHONY: setup-monitoring
setup-monitoring: ## Setup enhanced monitoring for load balancing and circuit breaking
	@echo "$(BLUE)Setting up enhanced monitoring...$(NC)"
	@mkdir -p monitoring
	@cat > monitoring/enhanced-prometheus.yml << 'EOF' && \
global: \
  scrape_interval: 15s \
  evaluation_interval: 15s \
\
scrape_configs: \
  - job_name: "api-gateway-enhanced" \
    static_configs: \
      - targets: ["gateway:8080"] \
    metrics_path: "/metrics" \
    scrape_interval: 10s \
    scrape_timeout: 5s \
\
  - job_name: "api-gateway-admin" \
    static_configs: \
      - targets: ["gateway:8080"] \
    metrics_path: "/admin/health-overview" \
    scrape_interval: 30s \
    scrape_timeout: 10s \
EOF \
	echo "Enhanced monitoring configuration created"

.PHONY: validate-enhanced
validate-enhanced: ## Validate enhanced features are working correctly
	@echo "$(BLUE)Validating enhanced features...$(NC)"
	@echo "1. Checking if load balancers are created..."
	@curl -s http://localhost:30080/admin/load-balancers | jq 'keys | length' | \
		{ read count; [ "$count" -gt 0 ] && echo "✅ Load balancers: $count found" || echo "❌ No load balancers found"; }
	@echo "2. Checking if circuit breakers are initialized..."
	@curl -s http://localhost:30080/admin/circuit-breakers | jq 'keys | length' | \
		{ read count; [ "$count" -gt 0 ] && echo "✅ Circuit breakers: $count found" || echo "❌ No circuit breakers found"; }
	@echo "3. Checking service health..."
	@curl -s http://localhost:30080/admin/health-overview | jq '.summary.service_health_rate' | \
		{ read rate; echo "✅ Service health rate: $rate%"; }
	@echo "4. Testing basic functionality..."
	@curl -f -s http://localhost:30080/health >/dev/null && echo "✅ Health endpoint working" || echo "❌ Health endpoint failed"
	@curl -f -s http://localhost:30080/products >/dev/null && echo "✅ Products endpoint working" || echo "❌ Products endpoint failed"

# =============================================================================
# Enhanced Help
# =============================================================================

.PHONY: help-enhanced
help-enhanced: ## Show help for enhanced features
	@echo "$(BLUE)Enhanced API Gateway Commands:$(NC)"
	@echo ""
	@echo "$(YELLOW)Testing:$(NC)"
	@echo "  $(GREEN)test-enhanced$(NC)      - Full enhanced features test"
	@echo "  $(GREEN)test-load-balancing$(NC) - Test load balancing only"
	@echo "  $(GREEN)test-circuit-breaker$(NC) - Test circuit breaker only"
	@echo "  $(GREEN)stress-test$(NC)        - Run stress test"
	@echo "  $(GREEN)benchmark-enhanced$(NC) - Benchmark performance"
	@echo ""
	@echo "$(YELLOW)Monitoring:$(NC)"
	@echo "  $(GREEN)monitor-admin$(NC)      - Show admin endpoints"
	@echo "  $(GREEN)watch-logs$(NC)         - Watch filtered logs"
	@echo "  $(GREEN)validate-enhanced$(NC)  - Validate features"
	@echo ""
	@echo "$(YELLOW)Deployment:$(NC)"
	@echo "  $(GREEN)deploy-enhanced$(NC)    - Build, deploy, and test"
	@echo "  $(GREEN)setup-monitoring$(NC)   - Setup enhanced monitoring"