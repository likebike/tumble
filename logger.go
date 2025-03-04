package tumble

import (
	"io"
	"path/filepath"
	"sync"
	"time"
)

const (
	compressSuffix = ".gz"
	fileMode       = 0644
)

// Ensure we always implement io.WriteCloser
var _ io.WriteCloser = (*Logger)(nil)

var (
	// These constants are mocked out by tests
	nowFn = time.Now
	MB    = uint(1024 * 1024)
)

func NewLogger(fpath string, maxLogSizeMB, maxTotalSizeMB uint, formatFn func(msg []byte, buf []byte) ([]byte, int)) *Logger {
	logger := &Logger{
		/* Filepath:       */ filepath.Clean(fpath),
		/* MaxLogSizeMB:   */ maxLogSizeMB,
		/* MaxTotalSizeMB: */ maxTotalSizeMB,
		/* FormatFn:       */ formatFn,

		/* file:           */ nil,
		/* size:           */ 0,
		/* millCh:         */ make(chan struct{}, 2),
		/* millWG:         */ sync.WaitGroup{},
		/* stopMillOnce:   */ sync.Once{},
		/* fmtbuf:         */ nil,
	}

	logger.millWG.Add(1)
	go logger.millRun()

	return logger
}

func (me *Logger) Write(p []byte) (n int, err error) {
	writeLen := int64(len(p))

	if me.file == nil {
		if err = me.openExistingOrNew(len(p)); err != nil {
			return 0, err
		}
	} else if me.size+writeLen > int64(me.MaxLogSizeMB*MB) {
		if err := me.rotate(); err != nil {
			return 0, err
		}
	}

	var msg []byte
	var msgIdx int
	if me.FormatFn != nil {
		me.fmtbuf = me.fmtbuf[:0]
		me.fmtbuf, msgIdx = me.FormatFn(p, me.fmtbuf)
		msg = me.fmtbuf
	} else {
		msg = p
	}

	n, err = me.file.Write(msg)
	me.size += int64(n)
	if me.FormatFn != nil {
		// Return length of p consumed
		if n < msgIdx {
			return 0, err
		}
		if n-msgIdx > len(p) {
			return len(p), err
		}
		return n - msgIdx, err
	}
	return n, err
}

func (me *Logger) closeFile() error {
	var ERR error

	if me.file == nil {
		return nil
	}

	err := me.Flush()
	if ERR == nil {
		ERR = err
	}

	err = me.file.Close()
	if ERR == nil {
		ERR = err
	}
	me.file = nil

	return ERR
}
func (me *Logger) Close() error {
	err := me.closeFile()
	me.StopMill()

	return err
}
