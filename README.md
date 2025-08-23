# SMS Gateway

A simple SMS gateway service written in Go.

## Features

* Users can top up a balance and send SMS.
* Messages are enqueued and processed by worker processes.
* Fair scheduling with **least-recently-served (LRS)** ordering prevents whales from starving smaller users.
* REST API for user and message operations.
* Background workers claim and deliver messages.
* Prometheus metrics and health endpoints.

## Requirements

* Go 1.22+
* PostgreSQL 16+

## Setup

1. Run migrations:

   ```bash
   make migrate
   ```

2. Start the API server:

   ```bash
   go run ./cmd/api
   ```

   It will listen on `:8080`.

3. Start the worker:

   ```bash
   go run ./cmd/worker
   ```

   It will process queued messages.

## API

* `POST /users` — create user
* `POST /users/{id}/topup` — add balance
* `GET /users/{id}/balance` — get balance
* `POST /messages` — enqueue SMS (requires `X-User-ID` header)
* `GET /messages` — list messages
* `GET /messages/{id}` — get message

## Health & Metrics

* API: `/healthz` and `/metrics` on port `8080`.
* Worker: `/healthz` and `/metrics` on port `9090`.