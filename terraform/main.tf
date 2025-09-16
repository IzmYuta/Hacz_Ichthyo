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

  depends_on = [google_project_service.apis]
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

  depends_on = [google_project_service.apis]
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
        value = google_sql_database_instance.main.private_ip_address
      }
      env {
        name  = "POSTGRES_PORT"
        value = "5432"
      }
      env {
        name  = "POSTGRES_USER"
        value = google_sql_user.main.name
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
        value = google_sql_database.main.name
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
        container_port = 3000
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      env {
        name  = "NEXT_PUBLIC_API_BASE"
        value = google_cloud_run_v2_service.api.uri
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

# Cloud Run service for LiveKit
resource "google_cloud_run_v2_service" "livekit" {
  name     = "livekit"
  location = var.region

  template {
    containers {
      image = "gcr.io/${var.project_id}/livekit:latest"
      
      ports {
        container_port = 7880
      }

      resources {
        limits = {
          cpu    = "2"
          memory = "2Gi"
        }
      }

      env {
        name  = "LIVEKIT_KEYS"
        value = "${google_secret_manager_secret.livekit_api_key.secret_id}:${google_secret_manager_secret.livekit_api_secret.secret_id}"
      }
    }

    scaling {
      min_instance_count = 0
      max_instance_count = 3
    }
  }

  depends_on = [google_project_service.apis]
}

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

resource "google_cloud_run_service_iam_policy" "livekit" {
  location = google_cloud_run_v2_service.livekit.location
  project  = google_cloud_run_v2_service.livekit.project
  service  = google_cloud_run_v2_service.livekit.name

  policy_data = data.google_iam_policy.public.policy_data
}

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
