terraform {
  required_providers {
    openai = { source = "orbital.scalr.io/providers/openai" }
  }
}

# Provisioning reads OPENAI_ADMIN_KEY. Never pass this credential to the application.
provider "openai" {}

variable "environment" {
  type        = string
  description = "Environment identifier, for example joy, preprod-uk or prod-us."
}

variable "project_id" {
  type        = string
  description = "Existing project for this environment."
}

variable "workload" {
  type        = string
  description = "Stable workload or feature name."
}

variable "permissions" {
  type        = set(string)
  description = "Approved inference permissions. Model request also permits other model capabilities; prefer responses.write alone for Responses-only workloads. Decisions permission covers explicit Decisions tasks."
  default     = ["api.responses.write", "api.model.request", "api.decisions.write"]
  validation {
    condition = length(var.permissions) > 0 && alltrue([
      for permission in var.permissions : contains(["api.responses.write", "api.model.request", "api.decisions.write"], permission)
    ])
    error_message = "This inference example accepts only responses.write, model.request and decisions.write; it cannot grant administration or key management."
  }
}

resource "openai_project_service_account" "runtime" {
  project_id                  = var.project_id
  name                        = "${var.environment}-${var.workload}"
  create_service_account_only = true
}

resource "openai_project_role" "inference" {
  project_id  = var.project_id
  name        = "${var.environment}-${var.workload}-inference"
  description = "Inference without account, billing or organisation administration"
  permissions = var.permissions
}

resource "openai_group" "runtime" {
  name = "${var.environment}-${var.workload}-runtime"
}

resource "openai_group_user" "runtime" {
  group_id = openai_group.runtime.id
  user_id  = openai_project_service_account.runtime.id
}

resource "openai_project_group_role_assignment" "inference" {
  project_id = var.project_id
  group_id   = openai_group.runtime.id
  role_id    = openai_project_role.inference.id
}

output "service_account_id" {
  value = openai_project_service_account.runtime.id
}
