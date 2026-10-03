package wizard

import "testing"

// TestVeleroScheduleInputValidateCronGuard pins the robfig/cron v3.0.1 panic
// on a bare time-zone prefix: Validate must return a schedule field error,
// never panic. Removing the guard in parseScheduleCron makes this test panic.
func TestVeleroScheduleInputValidateCronGuard(t *testing.T) {
	for _, sched := range []string{"CRON_TZ=UTC", "TZ=UTC", "CRON_TZ=", "TZ="} {
		t.Run(sched, func(t *testing.T) {
			in := &VeleroScheduleInput{Name: "nightly", Schedule: sched}
			errs := in.Validate()
			if len(errs) != 1 || errs[0].Field != "schedule" {
				t.Fatalf("want one schedule field error, got %+v", errs)
			}
		})
	}

	ok := &VeleroScheduleInput{Name: "nightly", Schedule: "CRON_TZ=UTC 0 3 * * *"}
	if errs := ok.Validate(); len(errs) != 0 {
		t.Fatalf("valid zoned schedule rejected: %+v", errs)
	}
}
