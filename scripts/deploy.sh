#!/bin/bash

set -e

IMAGE_NAME="api-gateway"
IMAGE_TAG="latest"

CONTAINER_NAME="api-gateway-instance"

echo "Cleaning current container..."
docker stop ${CONTAINER_NAME} || true
docker rm ${CONTAINER_NAME} || true
docker rmi ${IMAGE_NAME}:${IMAGE_TAG} || true

echo "Docker imajı oluşturuluyor: ${IMAGE_NAME}:${IMAGE_TAG}"
echo "Creating Docker image: ${IMAGE_NAME}:${IMAGE_TAG}"
docker build -t ${IMAGE_NAME}:${IMAGE_TAG} .

echo "Docker image created succesfully."

echo "Running Docker container..."
docker run -d -p 8080:8080 --name ${CONTAINER_NAME} ${IMAGE_NAME}:${IMAGE_TAG}

echo "API Gateway container started! Port: 8080"
echo "Container ID: $(docker ps -aqf "name=${CONTAINER_NAME}")"
echo "To follow up with the logs: docker logs -f ${CONTAINER_NAME}"
echo "To stop the container: docker stop ${CONTAINER_NAME}"
echo "To remove the container: docker rm ${CONTAINER_NAME}"
