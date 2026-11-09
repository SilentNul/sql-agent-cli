package ui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	glamour "charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
)

// 给Agent的名字上色
var agentNameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#5CC8FF")).Bold(true)

type Deps struct {
	Ask func(ctx context.Context, input string) (string, error)
}

type Message struct {
	Role     string
	Content  string
	Rendered string //渲染后的内容
}

type model struct {
	deps      Deps
	textinput textinput.Model
	viewport  viewport.Model
	messages  []Message
	loading   bool
	spinner   spinner.Model
	renderer  *glamour.TermRenderer
}

type answerMsg struct {
	content string
	err     error
}

// 调用LLM
func askAgent(deps Deps, input string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		answer, err := deps.Ask(ctx, input)
		return answerMsg{
			content: answer,
			err:     err,
		}
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m *model) refreshMessages() {
	var b strings.Builder

	for _, msg := range m.messages {
		if msg.Role != "" {
			b.WriteString(agentNameStyle.Render(msg.Role))
		}
		b.WriteString("> ")
		b.WriteString(msg.Rendered)
		b.WriteString("\n\n")
	}

	if m.loading {
		b.WriteString(m.spinner.View())
		b.WriteString("Agent正在思考...\n")
	}

	m.viewport.SetContent(b.String())
	m.viewport.GotoBottom()
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.MouseWheelMsg: //鼠标滚动
		switch msg.Mouse().Button {
		case tea.MouseWheelUp:
			m.viewport.ScrollUp(3)
		case tea.MouseWheelDown:
			m.viewport.ScrollDown(3)
		}
		return m, nil

	case tea.WindowSizeMsg: //窗口大小变化
		m.viewport.SetWidth(msg.Width)
		m.viewport.SetHeight(max(1, msg.Height-4))
		m.textinput.SetWidth(max(1, msg.Width-2))
		return m, nil
	}

	switch msg := msg.(type) {
	case spinner.TickMsg: //转圈动画
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg) //持续更新

		if m.loading { //渲染消息
			m.refreshMessages()
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)
	}

	//处理消息
	var inputCmd tea.Cmd
	m.textinput, inputCmd = m.textinput.Update(msg)
	if inputCmd != nil {
		cmds = append(cmds, inputCmd)
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			if m.loading {
				return m, nil
			}
			input := strings.TrimSpace(
				m.textinput.Value(),
			)
			if input == "" {
				return m, nil
			}
			m.messages = append(
				m.messages,
				Message{
					Role:     "",
					Content:  input,
					Rendered: input,
				},
			)
			m.textinput.Reset() //清空输入框
			m.loading = true
			m.refreshMessages() //刷新显示

			cmds = append(cmds, askAgent(m.deps, input)) //异步问agent
			cmds = append(cmds, m.spinner.Tick)          //启动转圈动画
		}

	case answerMsg:
		content := msg.content
		if msg.err != nil {
			content = "错误: " + msg.err.Error()
		}

		rendered, err := m.renderer.Render(content)
		if err != nil {
			rendered = content
		}

		m.messages = append(
			m.messages,
			Message{
				Role:     "Agent ",
				Content:  content,
				Rendered: rendered,
			},
		)
		m.loading = false
		m.refreshMessages()
	}
	return m, tea.Batch(cmds...)
}

func (m model) View() tea.View {
	var b strings.Builder
	b.WriteString("====== SQL Agent ======\n\n")
	b.WriteString(m.viewport.View())
	b.WriteString("\n")
	b.WriteString(m.textinput.View())

	v := tea.NewView(
		b.String(),
	)
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func Run(deps Deps) error {
	ti := textinput.New()
	ti.Placeholder = "输入问题，按 Enter 发送..."
	ti.Focus()

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#0FDC78"))

	renderer, err := glamour.NewTermRenderer(
		glamour.WithWordWrap(80),
	)
	if err != nil {
		return err
	}

	vp := viewport.New()
	m := &model{
		deps:      deps,
		textinput: ti,
		viewport:  vp,
		messages:  []Message{},
		spinner:   s,
		renderer:  renderer,
	}

	p := tea.NewProgram(m)
	_, err = p.Run()

	return err
}
