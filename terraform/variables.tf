variable "project_id" {
  description = "The GCP project ID"
  type        = string
  default     = "radio24-project"
}

variable "region" {
  description = "The GCP region"
  type        = string
  default     = "asia-northeast1"
}

variable "postgres_password" {
  description = "Password for the PostgreSQL database user"
  type        = string
  sensitive   = true
}

variable "openai_api_key" {
  description = "OpenAI API key"
  type        = string
  sensitive   = true
}

variable "livekit_api_key" {
  description = "LiveKit API key"
  type        = string
  sensitive   = true
}

variable "livekit_api_secret" {
  description = "LiveKit API secret"
  type        = string
  sensitive   = true
}

variable "livekit_url" {
  description = "LiveKit WebSocket URL"
  type        = string
  sensitive   = true
}

variable "postgres_host" {
  description = "PostgreSQL host"
  type        = string
  sensitive   = true
}

variable "postgres_port" {
  description = "PostgreSQL port"
  type        = string
  sensitive   = true
}

variable "postgres_user" {
  description = "PostgreSQL user"
  type        = string
  sensitive   = true
}

variable "postgres_db" {
  description = "PostgreSQL database name"
  type        = string
  sensitive   = true
}
