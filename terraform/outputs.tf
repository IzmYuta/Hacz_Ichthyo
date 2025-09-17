output "api_url" {
  description = "URL of the API service"
  value       = google_cloud_run_v2_service.api.uri
}

output "web_url" {
  description = "URL of the Web service"
  value       = google_cloud_run_v2_service.web.uri
}

# LiveKit Cloud URL is configured in terraform.tfvars

output "database_connection_name" {
  description = "Connection name for the Cloud SQL instance"
  value       = google_sql_database_instance.main.connection_name
}

output "database_private_ip" {
  description = "Private IP address of the Cloud SQL instance"
  value       = google_sql_database_instance.main.private_ip_address
}

output "redis_host" {
  description = "Host of the Redis instance"
  value       = google_redis_instance.main.host
}

output "vpc_connector_name" {
  description = "Name of the VPC connector"
  value       = google_vpc_access_connector.main.name
}
