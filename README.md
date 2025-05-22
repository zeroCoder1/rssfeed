# Suprnews - RSS Reader

A modern RSS reader with automatic categorization and content extraction.

## Deployment Instructions

### Prerequisites

- Docker
- Docker Compose

### Building and Running

1. Clone the repository
2. Navigate to the project directory
3. Run the following command to build and start the application:

```bash
docker-compose up -d
```

The application will be available at: http://localhost:8080

### Stopping the Application

```bash
docker-compose down
```

### Viewing Logs

```bash
docker-compose logs -f
```

### Updating the Application

To update the application with the latest changes:

```bash
git pull
docker-compose build
docker-compose down
docker-compose up -d
```

Or simply run:

```bash
./deploy.sh
```

## Data Persistence

The application data is stored in a Docker volume named `suprnews_data`. This ensures that your database and settings are preserved across container restarts and updates.
