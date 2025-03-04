# Translation Microservice

A Go-based microservice that provides text translation via Google Translate API with Redis caching.

## Features

- HTTP API for translation requests
- Automatic language detection (when source language is not specified)
- Redis caching with 2-week TTL
- Docker and Docker Compose support for easy deployment
- Health check endpoint

## Prerequisites

- Go 1.21 or higher
- Redis server
- Google Cloud Platform account with Translation API enabled
- Google Cloud service account credentials

## Setup

### 1. Clone the repository

```bash
git clone https://github.com/yourusername/translation-service.git
cd translation-service
```

### 2. Set up Google Cloud credentials

1. Create a Google Cloud project
2. Enable the Cloud Translation API
3. Create a service account with Translation API access
4. Download the service account key as JSON
5. Save the JSON file as `credentials.json` in the project root or set the `GOOGLE_APPLICATION_CREDENTIALS` environment variable to point to your credentials file

### 3. Configure environment variables

Create a `.env` file based on the example:

```bash
cp .env.example .env
```

Edit the `.env` file to match your configuration.

### 4. Run with Docker Compose

This is the easiest way to get up and running:

```bash
docker-compose up -d
```

### 5. Or run locally

If you prefer to run the service locally:

1. Start a Redis server
2. Install dependencies:
```bash
go mod download
```
3. Run the service:
```bash
go run main.go
```

## API Usage

### Translate Text

**Endpoint**: `POST /translate`

**Request Body**:

```json
{
  "text": "Hello, world!",
  "source_lang": "en",  // Optional: ISO 639-1 language code
  "target_lang": "es"   // Required: ISO 639-1 language code
}
```

If `source_lang` is omitted, the service will auto-detect the source language.

**Response**:

```json
{
  "translated_text": "¡Hola, mundo!",
  "source_lang": "en",
  "target_lang": "es",
  "cache_hit": false
}
```

## EXAMPLE `curl`

```
curl -X POST \
  http://localhost:8080/translate \
  -H "Content-Type: application/json" \
  -d '{
    "text": "Hello, world!",
    "source_lang": "en",
    "target_lang": "es"
  }' \
  -o response.json
```


### Health Check

**Endpoint**: `GET /health`

Returns `200 OK` if the service and Redis are functioning properly.

## Redis Caching

The service caches translation results in Redis with a 2-week TTL (time to live). The cache key is constructed using the source language, target language, and input text.

## Deployment Considerations

- For production deployments, consider adding authentication to the API
- Set up proper Redis security (password, firewall, etc.)
- Implement rate limiting to prevent excessive API usage
- Use environment variables for all configuration in production

## License

MIT
