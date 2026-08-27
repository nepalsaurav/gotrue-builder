package gotruectl

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

const dashboardRefreshInterval = 5 * time.Second

func newDashboardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dashboard",
		Short: "Live-updating terminal dashboard: postgres, every tenant, and their health",
		Long: `The same checks "doctor" runs once, refreshing automatically every 5s
in an interactive terminal view. Press r to refresh immediately, q or
ctrl+c to quit.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := tea.NewProgram(newDashboardModel()).Run()
			return err
		},
	}
}

type dashboardChecksMsg struct {
	checks []doctorCheck
}

type dashboardTickMsg time.Time

type dashboardModel struct {
	checks     []doctorCheck
	lastCheck  time.Time
	refreshing bool
}

func newDashboardModel() dashboardModel {
	return dashboardModel{}
}

func fetchDashboardChecks() tea.Cmd {
	return func() tea.Msg {
		return dashboardChecksMsg{checks: gatherDoctorChecks()}
	}
}

func tickDashboard() tea.Cmd {
	return tea.Tick(dashboardRefreshInterval, func(t time.Time) tea.Msg {
		return dashboardTickMsg(t)
	})
}

func (m dashboardModel) Init() tea.Cmd {
	return tea.Batch(fetchDashboardChecks(), tickDashboard())
}

func (m dashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "r":
			m.refreshing = true
			return m, fetchDashboardChecks()
		}
	case dashboardTickMsg:
		m.refreshing = true
		return m, tea.Batch(fetchDashboardChecks(), tickDashboard())
	case dashboardChecksMsg:
		m.refreshing = false
		m.lastCheck = time.Now()
		m.checks = msg.checks
	}
	return m, nil
}

func (m dashboardModel) View() string {
	title := headerStyle.Render("gotruectl dashboard")
	refreshState := fmt.Sprintf("refreshed %s · every %s", m.lastCheck.Format("15:04:05"), dashboardRefreshInterval)
	if m.refreshing {
		refreshState = "refreshing..."
	}
	if m.lastCheck.IsZero() {
		refreshState = "loading..."
	}
	sub := mutedStyle.Render(refreshState)
	footer := mutedStyle.Render("q quit   r refresh now")

	rows := make([][]string, 0, len(m.checks))
	for _, c := range m.checks {
		status := successStyle.Render("OK")
		switch {
		case c.warn:
			status = warnStyle.Render("WARN")
		case !c.ok:
			status = errorStyle.Render("FAIL")
		}
		rows = append(rows, []string{c.component, status, c.detail})
	}
	table := renderTable([]string{"COMPONENT", "STATUS", "DETAIL"}, rows)

	return lipgloss.JoinVertical(lipgloss.Left, title, sub, "", table, "", footer)
}
