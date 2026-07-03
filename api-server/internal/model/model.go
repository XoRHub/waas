// Package model holds the platform's persisted and API-facing entities.
package model

import (
	"time"

	"github.com/xorhub/waas/shared/auth"
)

// User is a platform account (local auth).
type User struct {
	ID            string     `json:"id"`
	Username      string     `json:"username"`
	Email         string     `json:"email,omitempty"`
	PasswordHash  string     `json:"-"`
	Role          auth.Role  `json:"role"`
	Active        bool       `json:"active"`
	MaxWorkspaces int        `json:"maxWorkspaces"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	LastLoginAt   *time.Time `json:"lastLoginAt,omitempty"`
}

// Session records one desktop connection through the proxy.
type Session struct {
	ID            string     `json:"id"`
	UserID        string     `json:"userId"`
	WorkspaceID   string     `json:"workspaceId"`
	WorkspaceName string     `json:"workspaceName"`
	Protocol      string     `json:"protocol"`
	ClientIP      string     `json:"clientIp,omitempty"`
	StartedAt     time.Time  `json:"startedAt"`
	EndedAt       *time.Time `json:"endedAt,omitempty"`
}

// AuditLog is one append-only audit trail entry.
type AuditLog struct {
	ID            string    `json:"id"`
	OccurredAt    time.Time `json:"occurredAt"`
	ActorID       string    `json:"actorId,omitempty"`
	ActorUsername string    `json:"actorUsername,omitempty"`
	Action        string    `json:"action"`
	ResourceType  string    `json:"resourceType"`
	ResourceID    string    `json:"resourceId,omitempty"`
	Detail        string    `json:"detail,omitempty"`
	ClientIP      string    `json:"clientIp,omitempty"`
}

// Workspace is the API projection of a Workspace CR.
type Workspace struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"displayName,omitempty"`
	TemplateRef string    `json:"templateRef"`
	OwnerID     string    `json:"ownerId"`
	Phase       string    `json:"phase"`
	OS          string    `json:"os,omitempty"`
	Protocol    string    `json:"protocol,omitempty"`
	Paused      bool      `json:"paused"`
	Message     string    `json:"message,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// WorkspaceTemplate is the API projection of a WorkspaceTemplate CR.
type WorkspaceTemplate struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	DisplayName string            `json:"displayName"`
	Description string            `json:"description,omitempty"`
	OS          string            `json:"os"`
	Image       string            `json:"image"`
	Port        int32             `json:"port,omitempty"`
	HomeSize    string            `json:"homeSize,omitempty"`
	Requests    map[string]string `json:"requests,omitempty"`
	Limits      map[string]string `json:"limits,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
}

// ConnectionInfo is what the WebSocket proxy needs to reach a desktop. It is
// only ever served on the internal service-to-service endpoint.
type ConnectionInfo struct {
	Protocol string `json:"protocol"`
	Hostname string `json:"hostname"`
	Port     int32  `json:"port"`
	Password string `json:"password,omitempty"`
	Username string `json:"username,omitempty"`
}
