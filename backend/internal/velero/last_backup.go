package velero

// A schedule's last-backup phase, read from its newest Backup (defect #5).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"

	"github.com/kubecenter/kubecenter/internal/auth"
)

// scheduleNameLabel is the label Velero puts on every backup a schedule
// creates.
const scheduleNameLabel = "velero.io/schedule-name"

// maxLabelValue is the longest label value Velero writes unchanged
// (validation.DNS1035LabelMaxLength); a longer name keeps labelHashLen hex
// characters of its hash.
const (
	maxLabelValue = 63
	labelHashLen  = 6
)

// scheduleLabelValue is the scheduleNameLabel value Velero writes for the
// schedule name, mirroring Velero's label.GetValidName: a name longer than
// 63 characters becomes its first 57 characters and the first six hex
// characters of its SHA-256.
func scheduleLabelValue(name string) string {
	if len(name) <= maxLabelValue {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	return name[:maxLabelValue-labelHashLen] + hex.EncodeToString(sum[:])[:labelHashLen]
}

// runTime orders a schedule's runs: the backup's start time, or its creation
// time when Velero has not started it yet. The start time leads because
// Velero's backup-sync controller recreates backups from the storage
// location, giving an old run a new creationTimestamp but its original
// startTimestamp.
func runTime(b *Backup) time.Time {
	if b.StartTime != nil {
		return *b.StartTime
	}
	return b.created
}

// newerRun reports whether a ran after b: by run time, then creation time,
// then name, since Velero names a schedule's runs <schedule>-<timestamp>.
func newerRun(a, b *Backup) bool {
	if ta, tb := runTime(a), runTime(b); !ta.Equal(tb) {
		return ta.After(tb)
	}
	if !a.created.Equal(b.created) {
		return a.created.After(b.created)
	}
	return a.Name > b.Name
}

type scheduleKey struct{ namespace, label string }

// withLastBackups returns a copy of schedules with LastBackupPhase and
// LastBackupOutcome set from each schedule's newest backup. A schedule with
// no backup keeps both empty. schedules is not modified: it may be the
// shared cache.
func withLastBackups(schedules []Schedule, backups []Backup) []Schedule {
	newest := make(map[scheduleKey]*Backup)
	for i := range backups {
		b := &backups[i]
		if b.ScheduleName == "" {
			continue
		}
		key := scheduleKey{b.Namespace, b.ScheduleName}
		if cur, ok := newest[key]; !ok || newerRun(b, cur) {
			newest[key] = b
		}
	}

	out := make([]Schedule, len(schedules))
	for i, s := range schedules {
		if b, ok := newest[scheduleKey{s.Namespace, scheduleLabelValue(s.Name)}]; ok {
			s.LastBackupPhase = b.Phase
			s.LastBackupOutcome = BackupOutcomeOf(b.Phase)
		}
		out[i] = s
	}
	return out
}

// canListBackups reports whether the user may list backups in namespace on
// the request's cluster; "" asks cluster-wide. A check that cannot be made
// counts as no: it only decides whether a schedule shows its last-backup
// phase, never whether it is served.
func (h *Handler) canListBackups(r *http.Request, user *auth.User, namespace string) bool {
	can, err := h.canAccess(r, user, "list", BackupGVR.Resource, namespace)
	return err == nil && can
}

// listableBackups keeps the schedule runs among backups that the user may
// list: all of them with cluster-wide access, otherwise those in namespaces
// the user may list backups in. The schedule detail reads its runs with a
// namespaced list as the user, so this gives the list the same answer
// (one check per namespace). backups is not modified: it may be the shared
// cache.
func (h *Handler) listableBackups(r *http.Request, user *auth.User, backups []Backup) []Backup {
	if h.canListBackups(r, user, "") {
		return backups
	}
	allowed := make(map[string]bool)
	var out []Backup
	for _, b := range backups {
		if b.ScheduleName == "" {
			continue
		}
		can, seen := allowed[b.Namespace]
		if !seen {
			can = h.canListBackups(r, user, b.Namespace)
			allowed[b.Namespace] = can
		}
		if can {
			out = append(out, b)
		}
	}
	return out
}

// withLastBackup sets one schedule's last-backup phase from a live list of
// its backups, read as the user on the request's cluster. When the list
// fails the phase stays empty and the schedule is still served.
func (h *Handler) withLastBackup(ctx context.Context, dyn dynamic.Interface, schedule *Schedule) {
	list, err := dyn.Resource(BackupGVR).Namespace(schedule.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: scheduleNameLabel + "=" + scheduleLabelValue(schedule.Name),
	})
	if err != nil {
		h.Logger.Debug("cannot read schedule backups", "namespace", schedule.Namespace, "name", schedule.Name, "error", err)
		return
	}
	backups := make([]Backup, 0, len(list.Items))
	for i := range list.Items {
		backups = append(backups, parseBackup(&list.Items[i]))
	}
	*schedule = withLastBackups([]Schedule{*schedule}, backups)[0]
}
