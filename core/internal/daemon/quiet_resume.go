package daemon

import (
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/matthewlu070111/smart-srun/core/internal/application"
	"github.com/matthewlu070111/smart-srun/core/internal/config"
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
	info, err := os.Lstat(paths.QuietResume())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != RuntimeFileMode || info.Size() > 4096 {
		return nil, quietRecordError(err)
	}
	f, err := os.Open(paths.QuietResume())
	if err != nil {
		return nil, quietRecordError(err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(data) > 4096 {
		return nil, quietRecordError(err)
	}
	var record quietRecord
	if err := config.DecodePatch(data, &record); err != nil || record.SchemaVersion != 1 {
		return nil, quietRecordError(err)
	}
	return &record.Resume, nil
}

func writeQuietResume(paths Paths, resume application.QuietResume) error {
	data, err := json.Marshal(quietRecord{SchemaVersion: 1, Resume: resume})
	if err != nil || len(data) > 4096 {
		return quietRecordError(err)
	}
	f, err := os.CreateTemp(paths.Runtime, ".quiet-uplink-*")
	if err != nil {
		return quietRecordError(err)
	}
	defer f.Close()
	defer os.Remove(f.Name())
	if err := f.Chmod(RuntimeFileMode); err != nil {
		return quietRecordError(err)
	}
	if _, err := f.Write(data); err != nil {
		return quietRecordError(err)
	}
	if err := f.Close(); err != nil {
		return quietRecordError(err)
	}
	if err := os.Rename(f.Name(), paths.QuietResume()); err != nil {
		return quietRecordError(err)
	}
	return nil
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
