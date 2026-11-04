package security

import "log/slog"

type AuditEvent struct {
	Event    string
	Subject  string
	DeviceID string
	Success  bool
}

func RecordAudit(logger *slog.Logger, event AuditEvent) {
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("security_audit", "event", event.Event, "subject", event.Subject, "device_id", event.DeviceID, "success", event.Success)
}
