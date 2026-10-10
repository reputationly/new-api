package quotaunitmigration

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/QuantumNous/new-api/common"
)

// report 迁移报告。dry-run 与 apply 输出同一套结构（§M1.8 / §M1.9），
// 区别只在「after」列是算出来的预计值还是落地后的实际值。
type report struct {
	out    io.Writer
	buf    *bytes.Buffer
	dryRun bool
	rate   float64
	errs   []error
}

func newReport(out io.Writer, dryRun bool, rate float64) *report {
	return &report{out: out, buf: &bytes.Buffer{}, dryRun: dryRun, rate: rate}
}

func (r *report) line(format string, args ...any) {
	fmt.Fprintf(r.buf, format+"\n", args...)
}

func (r *report) blank() {
	r.buf.WriteString("\n")
}

// table 输出一块对齐的表格，每块独立对齐，避免长报告里列宽被最长的行拉爆。
func (r *report) table(header []string, rows [][]string) {
	tw := tabwriter.NewWriter(r.buf, 0, 0, 2, ' ', 0)
	write := func(cells []string) {
		for i, c := range cells {
			if i > 0 {
				fmt.Fprint(tw, "\t")
			}
			fmt.Fprint(tw, c)
		}
		fmt.Fprintln(tw)
	}
	write(header)
	for _, row := range rows {
		write(row)
	}
	_ = tw.Flush()
}

// flush 把整份报告写出去。返回值让调用方决定退出码。
func (r *report) flush() error {
	if _, err := io.Copy(r.out, r.buf); err != nil {
		return err
	}
	if len(r.errs) > 0 {
		return r.errs[0]
	}
	return nil
}

// cny 把 quota 按「银行汇率」还原成人民币，用于报告里的金额对照。
func cny(quota float64, quotaPerUnit, rate float64) float64 {
	if quotaPerUnit <= 0 {
		return 0
	}
	return quota / quotaPerUnit * rate
}

func formatCNY(v float64) string { return fmt.Sprintf("%.6f", v) }

// unparsableIDFilePath 完整 id 清单的落盘位置：与运行日志同目录（启动时已创建）。
func unparsableIDFilePath() (string, error) {
	dir := "."
	if common.LogDir != nil && strings.TrimSpace(*common.LogDir) != "" {
		dir = *common.LogDir
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(abs, "quota-unit-migration-unparsable-ids.txt"), nil
}
