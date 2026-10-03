-- name: InsertAuditLog :exec
INSERT INTO audit_logs (user_id, action, entity, entity_id, metadata, ip)
VALUES ($1, $2, $3, $4, $5, $6);
