#!/bin/bash

echo "Cleaning up any old builds..."
docker-compose down --remove-orphans
docker system prune -f

echo "Building Docker image..."
docker-compose build --no-cache

echo "Stopping existing container..."
docker-compose down

echo "Starting new container..."
docker-compose up -d

echo "Deployment completed successfully!"

# Add container logs display
echo "Container logs (press Ctrl+C to exit):"
docker-compose logs -f
