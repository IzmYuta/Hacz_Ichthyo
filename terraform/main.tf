terraform {
  required_version = ">= 1.0"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}

# Get current project information
data "google_project" "current" {
  project_id = var.project_id
}

# Enable required APIs
resource "google_project_service" "apis" {
  for_each = toset([
    "cloudbuild.googleapis.com",
    "run.googleapis.com",
    "sqladmin.googleapis.com",
    "container.googleapis.com",
    "redis.googleapis.com",
    "compute.googleapis.com",
    "vpcaccess.googleapis.com",
    "secretmanager.googleapis.com"
  ])

  service = each.value
  disable_on_destroy = false
}

# Cloud SQL instance
resource "google_sql_database_instance" "main" {
  name             = "radio24-db"
  database_version = "POSTGRES_15"
  region           = var.region

  settings {
    tier = "db-f1-micro"
    
    disk_type       = "PD_SSD"
    disk_size       = 10
    disk_autoresize = true

    backup_configuration {
      enabled                        = true
      start_time                     = "03:00"
      point_in_time_recovery_enabled = true
    }

    ip_configuration {
      ipv4_enabled    = false
      private_network = google_compute_network.main.id
    }
  }

  deletion_protection = false

  depends_on = [google_project_service.apis, google_service_networking_connection.private_vpc_connection]
}

# Database
resource "google_sql_database" "main" {
  name     = "radio24"
  instance = google_sql_database_instance.main.name
}

# Database user
resource "google_sql_user" "main" {
  name     = "radio24-user"
  instance = google_sql_database_instance.main.name
  password = var.postgres_password
}

# Redis instance
resource "google_redis_instance" "main" {
  name           = "radio24-redis"
  tier           = "BASIC"
  memory_size_gb = 1
  region         = var.region

  depends_on = [google_project_service.apis, google_service_networking_connection.private_vpc_connection]
}

# VPC Network
resource "google_compute_network" "main" {
  name                    = "radio24-vpc"
  auto_create_subnetworks = false

  depends_on = [google_project_service.apis]
}

# Subnet
resource "google_compute_subnetwork" "main" {
  name          = "radio24-subnet"
  ip_cidr_range = "10.0.0.0/24"
  region        = var.region
  network       = google_compute_network.main.id
}

# Global Address for Service Networking
resource "google_compute_global_address" "private_ip_address" {
  name          = "radio24-private-ip"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 16
  network       = google_compute_network.main.id
}

# Service Networking Connection
resource "google_service_networking_connection" "private_vpc_connection" {
  network                 = google_compute_network.main.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.private_ip_address.name]
}

# VPC Connector for Cloud Run
resource "google_vpc_access_connector" "main" {
  name          = "radio24-connector"
  ip_cidr_range = "10.8.0.0/28"
  network       = google_compute_network.main.name
  region        = var.region

  depends_on = [google_project_service.apis]
}

# Cloud Run service for API
resource "google_cloud_run_v2_service" "api" {
  name     = "api"
  location = var.region

  template {
    containers {
      image = "gcr.io/${var.project_id}/api:latest"
      
      ports {
        container_port = 8080
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "1Gi"
        }
      }

      env {
        name  = "POSTGRES_HOST"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.postgres_host.secret_id
            version = "latest"
          }
        }
      }
      env {
        name  = "POSTGRES_PORT"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.postgres_port.secret_id
            version = "latest"
          }
        }
      }
      env {
        name  = "POSTGRES_USER"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.postgres_user.secret_id
            version = "latest"
          }
        }
      }
      env {
        name  = "POSTGRES_PASSWORD"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.postgres_password.secret_id
            version = "latest"
          }
        }
      }
      env {
        name  = "POSTGRES_DB"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.postgres_db.secret_id
            version = "latest"
          }
        }
      }
      env {
        name  = "OPENAI_API_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.openai_api_key.secret_id
            version = "latest"
          }
        }
      }
      env {
        name  = "LIVEKIT_API_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.livekit_api_key.secret_id
            version = "latest"
          }
        }
      }
      env {
        name  = "LIVEKIT_API_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.livekit_api_secret.secret_id
            version = "latest"
          }
        }
      }
      env {
        name  = "LIVEKIT_URL"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.livekit_url.secret_id
            version = "latest"
          }
        }
      }
      env {
        name  = "ALLOWED_ORIGIN"
        value = "https://web-${data.google_project.current.number}.${var.region}.run.app"
      }
    }

    scaling {
      min_instance_count = 0
      max_instance_count = 10
    }

    vpc_access {
      connector = google_vpc_access_connector.main.id
      egress    = "PRIVATE_RANGES_ONLY"
    }
  }

  depends_on = [google_project_service.apis]
}

# Cloud Run service for Web
resource "google_cloud_run_v2_service" "web" {
  name     = "web"
  location = var.region

  template {
    containers {
      image = "gcr.io/${var.project_id}/web:latest"
      
      ports {
        container_port = 8080
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      env {
        name  = "NEXT_PUBLIC_API_BASE"
        value = "https://api-${data.google_project.current.number}.${var.region}.run.app"
      }
      env {
        name  = "NEXT_PUBLIC_OPENAI_REALTIME_MODEL"
        value = "gpt-realtime"
      }
    }

    scaling {
      min_instance_count = 0
      max_instance_count = 5
    }
  }

  depends_on = [google_project_service.apis]
}

# Cloud Run service for Host
resource "google_cloud_run_v2_service" "host" {
  name     = "host"
  location = var.region

  template {
    containers {
      image = "gcr.io/${var.project_id}/host:latest"
      
      ports {
        container_port = 8080
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "1Gi"
        }
      }

      env {
        name  = "LIVEKIT_API_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.livekit_api_key.secret_id
            version = "latest"
          }
        }
      }
      env {
        name  = "LIVEKIT_API_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.livekit_api_secret.secret_id
            version = "latest"
          }
        }
      }
      env {
        name  = "OPENAI_API_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.openai_api_key.secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "LIVEKIT_WS_URL"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.livekit_url.secret_id
            version = "latest"
          }
        }
      }
    }

    scaling {
      min_instance_count = 1
      max_instance_count = 1
    }

    vpc_access {
      connector = google_vpc_access_connector.main.id
      egress    = "PRIVATE_RANGES_ONLY"
    }
  }

  depends_on = [google_project_service.apis]
}

# LiveKit Cloud is used instead of self-hosted LiveKit

# IAM policy for Cloud Run services
resource "google_cloud_run_service_iam_policy" "api" {
  location = google_cloud_run_v2_service.api.location
  project  = google_cloud_run_v2_service.api.project
  service  = google_cloud_run_v2_service.api.name

  policy_data = data.google_iam_policy.public.policy_data
}

resource "google_cloud_run_service_iam_policy" "web" {
  location = google_cloud_run_v2_service.web.location
  project  = google_cloud_run_v2_service.web.project
  service  = google_cloud_run_v2_service.web.name

  policy_data = data.google_iam_policy.public.policy_data
}

# LiveKit Cloud IAM policy not needed

data "google_iam_policy" "public" {
  binding {
    role = "roles/run.invoker"
    members = [
      "allUsers",
    ]
  }
}

# Secret Manager secrets
resource "google_secret_manager_secret" "postgres_password" {
  secret_id = "postgres-password"

  replication {
    auto {}
  }

  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret_version" "postgres_password" {
  secret      = google_secret_manager_secret.postgres_password.id
  secret_data = var.postgres_password
}

resource "google_secret_manager_secret" "openai_api_key" {
  secret_id = "openai-api-key"

  replication {
    auto {}
  }

  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret_version" "openai_api_key" {
  secret      = google_secret_manager_secret.openai_api_key.id
  secret_data = var.openai_api_key
}

resource "google_secret_manager_secret" "livekit_api_key" {
  secret_id = "livekit-api-key"

  replication {
    auto {}
  }

  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret_version" "livekit_api_key" {
  secret      = google_secret_manager_secret.livekit_api_key.id
  secret_data = var.livekit_api_key
}

resource "google_secret_manager_secret" "livekit_api_secret" {
  secret_id = "livekit-api-secret"

  replication {
    auto {}
  }

  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret_version" "livekit_api_secret" {
  secret      = google_secret_manager_secret.livekit_api_secret.id
  secret_data = var.livekit_api_secret
}

# LiveKit URL secret
resource "google_secret_manager_secret" "livekit_url" {
  secret_id = "livekit-url"

  replication {
    auto {}
  }

  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret_version" "livekit_url" {
  secret      = google_secret_manager_secret.livekit_url.id
  secret_data = var.livekit_url
}

# PostgreSQL connection secrets
resource "google_secret_manager_secret" "postgres_host" {
  secret_id = "postgres-host"

  replication {
    auto {}
  }

  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret_version" "postgres_host" {
  secret      = google_secret_manager_secret.postgres_host.id
  secret_data = var.postgres_host
}

resource "google_secret_manager_secret" "postgres_port" {
  secret_id = "postgres-port"

  replication {
    auto {}
  }

  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret_version" "postgres_port" {
  secret      = google_secret_manager_secret.postgres_port.id
  secret_data = var.postgres_port
}

resource "google_secret_manager_secret" "postgres_user" {
  secret_id = "postgres-user"

  replication {
    auto {}
  }

  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret_version" "postgres_user" {
  secret      = google_secret_manager_secret.postgres_user.id
  secret_data = var.postgres_user
}

resource "google_secret_manager_secret" "postgres_db" {
  secret_id = "postgres-db"

  replication {
    auto {}
  }

  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret_version" "postgres_db" {
  secret      = google_secret_manager_secret.postgres_db.id
  secret_data = var.postgres_db
}
