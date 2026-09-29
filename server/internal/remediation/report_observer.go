package remediation

// ReportObserver receives committed human-report transitions. Bodies and agent
// transcripts never cross this boundary.
type ReportObserver interface {
	ReportChanged(kind string, issueID, actorID, occurrence int64)
}

func (s *Service) SetReportObserver(observer ReportObserver) { s.reportObserver = observer }

func (s *Service) observeReport(kind string, issueID, actorID, occurrence int64) {
	if s.reportObserver == nil {
		return
	}
	s.reportObserver.ReportChanged(kind, issueID, actorID, occurrence)
}
