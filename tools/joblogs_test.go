package tools

import "testing"

func TestJobDiagnosticValidation(t *testing.T) {
	for _, args := range []map[string]interface{}{
		{}, {"job_id": float64(0)}, {"job_id": 1.5}, {"job_id": "42"},
		{"job_id": float64(42), "tail_lines": float64(5001)},
	} {
		if _, err := handleJobLogs(nil, args); err == nil {
			t.Errorf("accepted invalid arguments: %v", args)
		}
	}
	for _, args := range []map[string]interface{}{
		{"state": "INVALID"}, {"method": ""}, {"limit": -1.0}, {"job_id": 0.5},
	} {
		if _, err := handleQueryJobs(nil, args); err == nil {
			t.Errorf("accepted invalid query: %v", args)
		}
	}
}
