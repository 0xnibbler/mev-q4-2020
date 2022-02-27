package metrics

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/afiskon/promtail-client/promtail"
	"github.com/sirupsen/logrus"
)

type logrusHook struct {
	client promtail.Client
}

func (h *logrusHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h *logrusHook) Fire(e *logrus.Entry) error {
	var fn func(format string, args ...interface{})
	switch e.Level {
	case logrus.DebugLevel:
		fn = h.client.Debugf
	case logrus.InfoLevel:
		fn = h.client.Infof
	case logrus.WarnLevel:
		fn = h.client.Warnf
	case logrus.ErrorLevel:
		fn = h.client.Errorf
	default:
		return nil
	}

	var data []string
	for k, v := range e.Data {
		val := fmt.Sprint(v)
		if strings.Contains(val, " ") {
			val = "\"" + val + "\""
		}
		data = append(data, k+"="+val)
	}
	sort.Strings(data)

	s := "time=%s caller=%s %s message=\"%s\"\n"

	caller := "<nil>"
	if e.Caller != nil {
		caller = fmt.Sprintf("%s:%d", path.Base(e.Caller.File), e.Caller.Line)
	}

	args := []interface{}{
		e.Time.Format(time.RFC3339Nano),
		caller,
		strings.Join(data, " "),
		strings.TrimSpace(e.Message)}

	if On {
		fn(s, args...)
	}

	fmt.Printf(colorLevel(e.Level)+"\t"+s, args...)

	return nil
}

func colorLevel(level logrus.Level) string {
	var color int
	switch level {
	case logrus.DebugLevel, logrus.TraceLevel:
		color = 37
	case logrus.WarnLevel:
		color = 33
	case logrus.ErrorLevel, logrus.FatalLevel, logrus.PanicLevel:
		color = 31
	default:
		color = 36
	}

	return fmt.Sprintf("\u001B[%dm%s\u001B[0m", color, strings.ToUpper(level.String()[:4]))
}
