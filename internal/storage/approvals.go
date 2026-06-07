package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// CreateApprovalRequest stores a pending local approval request.
func (d *DB) CreateApprovalRequest(req ApprovalRequest) (string, error) {
	if strings.TrimSpace(req.Action) == "" {
		return "", fmt.Errorf("approval action is required")
	}
	now := time.Now().UTC()
	if req.RequestID == "" {
		req.RequestID = generatedID("apr")
	}
	if req.Status == "" {
		req.Status = "pending"
	}
	_, err := d.db.Exec(`INSERT INTO approval_requests(request_id,policy_decision_id,workload_id,run_id,source,model,project,action,target,actor_role,status,reason,request_payload,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		req.RequestID, req.PolicyDecisionID, req.WorkloadID, req.RunID, req.Source, req.Model, req.Project, req.Action, req.Target,
		req.ActorRole, normalizeApprovalStatus(req.Status), req.Reason, req.RequestPayload, now, now)
	if err != nil {
		return "", err
	}
	return req.RequestID, nil
}

// ListApprovalRequests returns recent approval requests by status.
func (d *DB) ListApprovalRequests(status string, limit int) ([]ApprovalRequest, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := `SELECT request_id,policy_decision_id,workload_id,run_id,source,model,project,action,target,actor_role,status,reason,request_payload,
		created_at,updated_at,COALESCE(decided_at,''),decided_by,decision_note FROM approval_requests`
	args := []interface{}{}
	if status != "" && status != "all" {
		q += ` WHERE status=?`
		args = append(args, normalizeApprovalStatus(status))
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := d.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ApprovalRequest
	for rows.Next() {
		var r ApprovalRequest
		if err := rows.Scan(&r.RequestID, &r.PolicyDecisionID, &r.WorkloadID, &r.RunID, &r.Source, &r.Model, &r.Project,
			&r.Action, &r.Target, &r.ActorRole, &r.Status, &r.Reason, &r.RequestPayload, &r.CreatedAt, &r.UpdatedAt,
			&r.DecidedAt, &r.DecidedBy, &r.DecisionNote); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if out == nil {
		out = []ApprovalRequest{}
	}
	return out, rows.Err()
}

// ResolveApprovalRequest marks a request approved or rejected.
func (d *DB) ResolveApprovalRequest(requestID, status, decidedBy, note string) error {
	if requestID == "" {
		return fmt.Errorf("request_id is required")
	}
	status = normalizeApprovalStatus(status)
	if status != "approved" && status != "rejected" {
		return fmt.Errorf("approval status must be approved or rejected")
	}
	now := time.Now().UTC()
	res, err := d.db.Exec(`UPDATE approval_requests SET status=?,updated_at=?,decided_at=?,decided_by=?,decision_note=?
		WHERE request_id=? AND status='pending'`, status, now, now, decidedBy, note, requestID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ApprovalAllows reports whether a previously approved request can authorize an operation retry.
func (d *DB) ApprovalAllows(requestID, action, target string) (bool, error) {
	if requestID == "" {
		return false, nil
	}
	var storedAction, storedTarget, status string
	err := d.db.QueryRow(`SELECT action,target,status FROM approval_requests WHERE request_id=?`, requestID).Scan(&storedAction, &storedTarget, &status)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if status != "approved" {
		return false, nil
	}
	if !strings.EqualFold(storedAction, action) {
		return false, nil
	}
	if storedTarget != "" && target != "" && storedTarget != target {
		return false, nil
	}
	return true, nil
}

func normalizeApprovalStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "approved", "approve", "allow":
		return "approved"
	case "rejected", "reject", "denied", "deny":
		return "rejected"
	case "all":
		return "all"
	default:
		return "pending"
	}
}
