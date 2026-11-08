package control

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"agent-platform/backend/internal/model/runtimev1"
	"github.com/jackc/pgx/v5"
)

type Client struct{}

func (Client) IssueScope(ctx context.Context, tx pgx.Tx, in ScopeInput) (AuthorizedScope, error) {
	var result AuthorizedScope
	err := jsonCall(ctx, tx, "SELECT control.issue_scope($1::jsonb)", in, &result)
	return result, err
}

// AssertScope acquires authority locks only. Repositories must subsequently
// check their own Job/Task lease in this same transaction before business DML.
func (Client) AssertScope(ctx context.Context, tx pgx.Tx, ref runtimev1.ExecRef, capability, target, purpose string) (AuthorizedScope, error) {
	var raw []byte
	var result AuthorizedScope
	err := tx.QueryRow(ctx, "SELECT control.assert_scope($1,$2,$3,$4,$5)", ref.ScopeID, ref.ScopeHash, capability, target, purpose).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &result)
	}
	return result, err
}

// ReserveAttempt must be the first authority acquisition in a short admission
// transaction. Calling AssertScope first would introduce a SHARE-lock upgrade.
// After this call the repository checks/locks the current local lease, then
// commits. Network requests always take place outside that transaction.
func (Client) ReserveAttempt(ctx context.Context, tx pgx.Tx, in AttemptInput) (Attempt, error) {
	var result Attempt
	job, operation := "", ""
	switch in.Ref.OwnerKind {
	case "job", "task", "dispatch":
		job = in.Ref.OwnerID
	case "operation":
		operation = in.Ref.OwnerID
	default:
		return result, fmt.Errorf("unsupported execution owner kind")
	}
	input := struct {
		AttemptID    string      `json:"attempt_id"`
		ScopeID      string      `json:"scope_id"`
		ScopeHash    string      `json:"scope_hash"`
		LogicalKey   string      `json:"logical_call_key"`
		AttemptNo    int         `json:"attempt_no"`
		Capability   string      `json:"capability"`
		Target       string      `json:"target"`
		JobID        string      `json:"owner_job_id,omitempty"`
		OperationID  string      `json:"owner_operation_id,omitempty"`
		Epoch        int64       `json:"owner_epoch"`
		ResourceKeys []string    `json:"resource_keys"`
		Reservation  Reservation `json:"reservation"`
		HoldUntil    time.Time   `json:"hold_until"`
	}{
		AttemptID: in.AttemptID, ScopeID: in.Ref.ScopeID, ScopeHash: in.Ref.ScopeHash,
		LogicalKey: in.LogicalKey, AttemptNo: in.AttemptNo, Capability: in.Capability, Target: in.Target,
		JobID: job, OperationID: operation, Epoch: in.Ref.Lease.Epoch,
		ResourceKeys: in.ResourceKeys, Reservation: in.Reservation, HoldUntil: in.HoldUntil,
	}
	err := jsonCall(ctx, tx, "SELECT control.reserve_attempt($1::jsonb)", input, &result)
	return result, err
}

func (Client) SettleAttempt(ctx context.Context, tx pgx.Tx, in Settlement) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	var changed bool
	return tx.QueryRow(ctx, "SELECT control.settle_attempt($1::jsonb)", b).Scan(&changed)
}

func jsonCall(ctx context.Context, tx pgx.Tx, query string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	var raw []byte
	if err = tx.QueryRow(ctx, query, b).Scan(&raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
