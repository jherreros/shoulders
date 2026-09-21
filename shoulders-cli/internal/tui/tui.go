package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/pterm/pterm"
)

var (
	headerStyle = pterm.NewStyle(pterm.FgLightCyan, pterm.Bold)
	okPrefix    = pterm.NewStyle(pterm.FgGreen).Sprint("✓")
	failPrefix  = pterm.NewStyle(pterm.FgRed).Sprint("✗")
	waitPrefix  = pterm.NewStyle(pterm.FgYellow).Sprint("●")
	skipPrefix  = pterm.NewStyle(pterm.FgGray).Sprint("○")
	dimStyle    = pterm.NewStyle(pterm.FgGray)
	boldStyle   = pterm.NewStyle(pterm.Bold)
	redStyle    = pterm.NewStyle(pterm.FgRed)
	greenStyle  = pterm.NewStyle(pterm.FgGreen)
)

// phaseState tracks timing for a single phase.
type phaseState struct {
	Name     string
	start    time.Time
	duration time.Duration
	done     bool
	failed   bool
}

// PhaseTracker displays live progress for a sequence of phases.
type PhaseTracker struct {
	phases     []phaseState
	current    int
	startTime  time.Time
	verbose    bool
	area       *pterm.AreaPrinter
	ticker     *time.Ticker
	stopTick   chan struct{}
	lastDetail string
}

// NewPhaseTracker creates a new tracker for the given phase names.
// If verbose is true, detail messages are shown for all phases.
func NewPhaseTracker(names []string, verbose bool) *PhaseTracker {
	phases := make([]phaseState, len(names))
	for i, n := range names {
		phases[i] = phaseState{Name: n}
	}
	area, _ := pterm.DefaultArea.WithRemoveWhenDone(false).Start()
	now := time.Now()

	pt := &PhaseTracker{
		phases:    phases,
		current:   -1,
		startTime: now,
		verbose:   verbose,
		area:      area,
		stopTick:  make(chan struct{}),
	}

	// Background ticker refreshes the elapsed time every second.
	pt.ticker = time.NewTicker(1 * time.Second)
	go func() {
		for {
			select {
			case <-pt.ticker.C:
				pt.refresh()
			case <-pt.stopTick:
				return
			}
		}
	}()

	return pt
}

// Start marks the next phase as in progress and re-renders. It grows the
// phase list defensively so a caller with more steps than names fails
// loudly in the UI instead of panicking.
func (pt *PhaseTracker) Start(detail string) {
	pt.current++
	if pt.current >= len(pt.phases) {
		pt.phases = append(pt.phases, phaseState{Name: "Additional step"})
	}
	pt.phases[pt.current].start = time.Now()
	pt.render(detail)
}

// UpdateDetail re-renders with an updated detail string for the current phase.
func (pt *PhaseTracker) UpdateDetail(detail string) {
	pt.render(detail)
}

// Complete marks the current phase as done and re-renders.
func (pt *PhaseTracker) Complete() {
	p := &pt.phases[pt.current]
	p.duration = time.Since(p.start)
	p.done = true
	pt.render("")
}

// Fail marks the current phase as failed and re-renders.
func (pt *PhaseTracker) Fail(detail string) {
	p := &pt.phases[pt.current]
	p.duration = time.Since(p.start)
	p.failed = true
	pt.render("FAIL: " + detail)
}

// Stop finalises the area printer and stops the background ticker.
func (pt *PhaseTracker) Stop() {
	pt.ticker.Stop()
	close(pt.stopTick)
	_ = pt.area.Stop()
}

// Elapsed returns the total time since the tracker was created.
func (pt *PhaseTracker) Elapsed() time.Duration {
	return time.Since(pt.startTime)
}

// Summary returns a final summary string like "Shoulders platform provisioned in 04:32".
func (pt *PhaseTracker) Summary() string {
	return greenStyle.Sprintf("  Shoulders platform provisioned in %s", FormatDuration(pt.Elapsed()))
}

// Verbose returns whether verbose mode is enabled.
func (pt *PhaseTracker) Verbose() bool {
	return pt.verbose
}

// refresh re-renders with the last detail (for timer updates).
func (pt *PhaseTracker) refresh() {
	pt.render("")
}

func (pt *PhaseTracker) render(detail string) {
	if detail != "" {
		pt.lastDetail = detail
	}
	activeDetail := pt.lastDetail
	// Clear lastDetail when a phase completes so the next phase starts clean.
	if pt.current >= 0 && pt.phases[pt.current].done {
		pt.lastDetail = ""
		activeDetail = ""
	}

	elapsed := FormatDuration(time.Since(pt.startTime))
	out := headerStyle.Sprintf("  Shoulders Platform Bootstrap") +
		"  " + dimStyle.Sprintf("[%s]", elapsed) + "\n\n"

	for i, phase := range pt.phases {
		var prefix string
		var line string
		switch {
		case phase.done:
			prefix = okPrefix
			dur := dimStyle.Sprintf("(%s)", FormatDuration(phase.duration))
			line = phase.Name + "  " + dur
		case phase.failed:
			prefix = failPrefix
			dur := dimStyle.Sprintf("(%s)", FormatDuration(phase.duration))
			line = phase.Name + "  " + dur + "  " + redStyle.Sprint(activeDetail)
		case i == pt.current:
			prefix = waitPrefix
			phaseDur := dimStyle.Sprintf("(%s)", FormatDuration(time.Since(phase.start)))
			line = boldStyle.Sprint(phase.Name) + "  " + phaseDur
			if activeDetail != "" {
				line += "  " + dimStyle.Sprint(activeDetail)
			}
		default:
			prefix = skipPrefix
			line = dimStyle.Sprint(phase.Name)
		}
		out += fmt.Sprintf("  %s %s\n", prefix, line)
	}

	pt.area.Update(out)
}

// FormatDuration formats a duration as MM:SS.
func FormatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d", m, s)
}

// StatusLine renders a single labelled status line.
func StatusLine(label string, ready bool, detail string) string {
	prefix := okPrefix
	status := greenStyle.Sprint("Healthy")
	if !ready {
		prefix = failPrefix
		status = redStyle.Sprint("Unhealthy")
	}
	line := fmt.Sprintf("  %s %-14s %s", prefix, label, status)
	if detail != "" {
		line += "  " + dimStyle.Sprint(detail)
	}
	return line
}

// Header renders a styled section header.
func Header(title string) string {
	return headerStyle.Sprintf("  %s", title)
}

// VerboseLines formats a slice of detail strings as indented verbose output.
// Returns an empty string when verbose is false or details is empty.
func VerboseLines(verbose bool, details []string) string {
	if !verbose || len(details) == 0 {
		return ""
	}
	var b strings.Builder
	for _, d := range details {
		b.WriteString("      " + dimStyle.Sprint(d) + "\n")
	}
	return b.String()
}
