package daemon

import (
	"errors"
	"os"

	"github.com/matthewlu070111/smart-srun/core/internal/application"
	"github.com/matthewlu070111/smart-srun/core/internal/domain"
	"github.com/matthewlu070111/smart-srun/core/internal/policy"
)

type quietRecord struct {
	SchemaVersion int                     `json:"schema_version"`
	Resume        application.QuietResume `json:"resume"`
}

func quietRecordError(err error) error {
	return domain.Errorf(domain.CodeInternal, "无法保存或读取临时切网记录").Wrap(err)
}

func readQuietResume(paths Paths) (*application.QuietResume, error) {
	var record quietRecord
	exists, err := readRuntimeRecord(paths.QuietResume(), &record)
	if err != nil || !exists {
		return nil, err
	}
	if record.SchemaVersion != 1 {
		return nil, quietRecordError(nil)
	}
	return &record.Resume, nil
}

func writeQuietResume(paths Paths, resume application.QuietResume) error {
	return writeRuntimeRecord(paths.QuietResume(), quietRecord{SchemaVersion: 1, Resume: resume})
}

func clearQuietResume(paths Paths) error {
	if err := os.Remove(paths.QuietResume()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return quietRecordError(err)
	}
	return nil
}

// Only the coordinator writes this record: finalizing a successful explicit
// switch or publishing cancellation cannot race a maintenance-loop write.
func (d *Daemon) finishQuietRecord(action application.Action, outcome application.Outcome) application.Outcome {
	var err error
	if action.Request.Kind == application.KindQuietHotspot && !outcome.MaintenanceDeferred {
		cfg := d.config.Snapshot()
		quiet := policy.EvaluateQuiet(cfg.Quiet, action.StartedAt)
		if !quiet.Active {
			err = quietRecordError(nil)
		} else {
			err = writeQuietResume(d.paths, application.QuietResume{Revision: cfg.Revision,
				Occurrence: quiet.Occurrence, AccountID: action.Request.AccountID,
				HotspotID: action.Request.HotspotID, StartedAt: action.StartedAt})
		}
	} else {
		err = clearQuietResume(d.paths)
	}
	if err != nil {
		d.onError(err)
		outcome.State, outcome.Code = application.StateFailed, domain.CodeInternal
		outcome.Message = "线路切换已执行，但临时切网记录未能保存，请检查当前连接"
	}
	return outcome
}
