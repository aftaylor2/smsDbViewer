package main

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type viewState int

const (
	viewConversations viewState = iota
	viewMessages
	viewSearch
	viewAttachments
)

type model struct {
	store    *Store
	contacts *ContactBook
	state    viewState
	width    int
	height   int
	err      error

	convList  list.Model
	convItems []Conversation

	viewport           viewport.Model
	messages           []Message
	activeChatID       int
	activeChatTitle    string
	activeParticipants []string // raw handle IDs for the active chat
	activeMsgCount     int
	oldestCursor       int
	allLoaded          bool
	loading            bool

	// Search state
	searchInput   textinput.Model
	searchResults list.Model
	searching     bool
	searchTerm    string

	// Export state
	exporting    bool
	exportStatus string

	// Attachment list state
	attachmentList list.Model
}

// Bubble Tea messages
type conversationsLoadedMsg struct {
	conversations []Conversation
	err           error
}

type messagesLoadedMsg struct {
	messages []Message
	chatID   int
	prepend  bool
	err      error
}

type searchResultsMsg struct {
	results []SearchResult
	term    string
	err     error
}

type exportDoneMsg struct {
	path string
	err  error
}

type attachmentsLoadedMsg struct {
	attachments []ChatAttachment
	err         error
}

type attachmentOpenedMsg struct {
	err error
}

// convItem adapts Conversation for bubbles/list
type convItem struct {
	conv     Conversation
	contacts *ContactBook
}

func (c convItem) Title() string {
	if c.conv.DisplayName != "" {
		return c.conv.DisplayName
	}
	if c.contacts != nil && len(c.conv.Participants) > 0 {
		var names []string
		for _, p := range c.conv.Participants {
			names = append(names, c.contacts.ResolveName(p))
		}
		return strings.Join(names, ", ")
	}
	if len(c.conv.Participants) > 0 {
		return strings.Join(c.conv.Participants, ", ")
	}
	return c.conv.Identifier
}

func (c convItem) Description() string {
	last := "no messages"
	if !c.conv.LastMsgDate.IsZero() {
		last = formatRelativeDate(c.conv.LastMsgDate)
	}
	started := ""
	if !c.conv.FirstMsgDate.IsZero() {
		started = c.conv.FirstMsgDate.Format("Jan 02, 2006")
	}
	msgStats := fmt.Sprintf("%d msgs (%d sent, %d recv)", c.conv.MessageCount, c.conv.SentCount, c.conv.ReceivedCount)
	return fmt.Sprintf("%-14s |  %-36s |  started %s  |  %s",
		last, msgStats, started, c.conv.ServiceName)
}

func (c convItem) FilterValue() string {
	return c.Title()
}

// searchItem adapts SearchResult for bubbles/list
type searchItem struct {
	result SearchResult
}

func (s searchItem) Title() string {
	sender := "Me"
	if !s.result.IsFromMe {
		sender = s.result.Sender
		if sender == "" {
			sender = "Unknown"
		}
	}
	text := s.result.Text
	if text == "" {
		text = "[attachment]"
	}
	if len(text) > 80 {
		text = text[:80] + "..."
	}
	return fmt.Sprintf("%s: %s", sender, text)
}

func (s searchItem) Description() string {
	return fmt.Sprintf("in %s  |  %s", s.result.ChatName, formatRelativeDate(s.result.Date))
}

func (s searchItem) FilterValue() string {
	return s.result.Text
}

// attachmentItem adapts ChatAttachment for bubbles/list
type attachmentItem struct {
	attachment ChatAttachment
	contacts   *ContactBook
}

func (a attachmentItem) Title() string {
	parts := []string{a.attachment.TypeLabel}
	if a.attachment.Filename != "" {
		parts = append(parts, a.attachment.Filename)
	}
	if a.attachment.Size > 0 {
		parts = append(parts, formatBytes(a.attachment.Size))
	}
	return strings.Join(parts, " — ")
}

func (a attachmentItem) Description() string {
	sender := "Me"
	if !a.attachment.IsFromMe {
		if a.contacts != nil {
			sender = a.contacts.ResolveName(a.attachment.Sender)
		} else {
			sender = a.attachment.Sender
		}
		if sender == "" {
			sender = "Unknown"
		}
	}
	return fmt.Sprintf("from %s  |  %s", sender, formatRelativeDate(a.attachment.Date))
}

func (a attachmentItem) FilterValue() string {
	return a.attachment.Filename + " " + a.attachment.TypeLabel
}

func formatRelativeDate(t time.Time) string {
	now := time.Now()
	diff := now.Sub(t)

	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		return fmt.Sprintf("%dm ago", int(diff.Minutes()))
	case diff < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(diff.Hours()))
	case diff < 7*24*time.Hour:
		return t.Format("Mon 03:04 PM")
	case t.Year() == now.Year():
		return t.Format("Jan 02")
	default:
		return t.Format("Jan 02, 2006")
	}
}

func formatMessageTime(t time.Time) string {
	now := time.Now()
	hour := t.Hour() % 12
	if hour == 0 {
		hour = 12
	}
	ampm := "AM"
	if t.Hour() >= 12 {
		ampm = "PM"
	}
	timeStr := fmt.Sprintf("%02d:%02d %s", hour, t.Minute(), ampm)
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return timeStr
	}
	if t.Year() == now.Year() {
		return fmt.Sprintf("%s, %s", t.Format("Jan 02"), timeStr)
	}
	return fmt.Sprintf("%s, %s", t.Format("Jan 02, 2006"), timeStr)
}

func formatAttachments(attachments []AttachmentInfo) string {
	var parts []string
	for _, a := range attachments {
		parts = append(parts, a.String())
	}
	return strings.Join(parts, " ")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-1] + "~"
}

func NewModel(store *Store, contacts *ContactBook) model {
	delegate := list.NewDefaultDelegate()
	convList := list.New([]list.Item{}, delegate, 0, 0)
	convList.Title = "iMessage Conversations"
	convList.SetShowStatusBar(true)
	convList.SetFilteringEnabled(true)
	convList.Styles.Title = titleStyle

	vp := viewport.New(0, 0)
	vp.MouseWheelEnabled = true

	ti := textinput.New()
	ti.Placeholder = "Search all messages..."
	ti.CharLimit = 256
	ti.Width = 40

	searchDelegate := list.NewDefaultDelegate()
	searchList := list.New([]list.Item{}, searchDelegate, 0, 0)
	searchList.Title = "Search Results"
	searchList.SetShowStatusBar(true)
	searchList.SetFilteringEnabled(false)
	searchList.Styles.Title = titleStyle

	attachDelegate := list.NewDefaultDelegate()
	attachList := list.New([]list.Item{}, attachDelegate, 0, 0)
	attachList.Title = "Attachments"
	attachList.SetShowStatusBar(true)
	attachList.SetFilteringEnabled(true)
	attachList.Styles.Title = titleStyle

	return model{
		store:          store,
		contacts:       contacts,
		state:          viewConversations,
		convList:       convList,
		viewport:       vp,
		searchInput:    ti,
		searchResults:  searchList,
		attachmentList: attachList,
	}
}

func (m model) Init() tea.Cmd {
	return func() tea.Msg {
		convs, err := m.store.FetchConversations()
		return conversationsLoadedMsg{conversations: convs, err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.convList.SetSize(msg.Width-4, msg.Height-4)
		m.searchResults.SetSize(msg.Width-4, msg.Height-7)
		m.attachmentList.SetSize(msg.Width-4, msg.Height-4)
		m.viewport.Width = msg.Width - 4
		m.viewport.Height = calcViewportHeight(m.height, len(m.activeParticipants))
		if m.state == viewMessages && len(m.messages) > 0 {
			m.viewport.SetContent(m.renderMessages())
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		}

		switch m.state {
		case viewConversations:
			return m.updateConversationList(msg)
		case viewMessages:
			return m.updateMessageView(msg)
		case viewSearch:
			return m.updateSearchView(msg)
		case viewAttachments:
			return m.updateAttachmentView(msg)
		}

	case conversationsLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		m.convItems = msg.conversations
		items := make([]list.Item, len(msg.conversations))
		for i, c := range msg.conversations {
			items[i] = convItem{conv: c, contacts: m.contacts}
		}
		cmd := m.convList.SetItems(items)
		return m, cmd

	case messagesLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		if msg.chatID != m.activeChatID {
			return m, nil
		}
		m.loading = false
		if len(msg.messages) == 0 {
			m.allLoaded = true
			m.viewport.SetContent(m.renderMessages())
			return m, nil
		}
		if msg.prepend {
			m.messages = append(msg.messages, m.messages...)
		} else {
			m.messages = msg.messages
		}
		if len(m.messages) > 0 {
			m.oldestCursor = m.messages[0].ROWID
		}
		if len(msg.messages) < messagesPageSize {
			m.allLoaded = true
		}
		m.viewport.SetContent(m.renderMessages())
		if !msg.prepend {
			m.viewport.GotoBottom()
		}
		return m, nil

	case exportDoneMsg:
		m.exporting = false
		if msg.err != nil {
			m.exportStatus = fmt.Sprintf("Export failed: %v", msg.err)
		} else {
			m.exportStatus = fmt.Sprintf("Exported to %s", msg.path)
		}
		return m, nil

	case attachmentsLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		items := make([]list.Item, len(msg.attachments))
		for i, a := range msg.attachments {
			items[i] = attachmentItem{attachment: a, contacts: m.contacts}
		}
		cmd := m.attachmentList.SetItems(items)
		m.attachmentList.Title = fmt.Sprintf("Attachments — %d files", len(msg.attachments))
		return m, cmd

	case attachmentOpenedMsg:
		if msg.err != nil {
			m.exportStatus = fmt.Sprintf("Failed to open: %v", msg.err)
		}
		return m, nil

	case searchResultsMsg:
		m.searching = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.searchTerm = msg.term
		items := make([]list.Item, len(msg.results))
		for i, r := range msg.results {
			items[i] = searchItem{result: r}
		}
		cmd := m.searchResults.SetItems(items)
		m.searchResults.Title = fmt.Sprintf("Search Results — %d matches for %q", len(msg.results), msg.term)
		return m, cmd
	}

	switch m.state {
	case viewConversations:
		var cmd tea.Cmd
		m.convList, cmd = m.convList.Update(msg)
		return m, cmd
	case viewMessages:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	case viewSearch:
		if m.searchInput.Focused() {
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			return m, cmd
		}
		var cmd tea.Cmd
		m.searchResults, cmd = m.searchResults.Update(msg)
		return m, cmd
	case viewAttachments:
		var cmd tea.Cmd
		m.attachmentList, cmd = m.attachmentList.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m model) updateConversationList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		selected, ok := m.convList.SelectedItem().(convItem)
		if !ok {
			return m, nil
		}
		m.state = viewMessages
		m.activeChatID = selected.conv.ChatID
		m.activeChatTitle = selected.Title()
		m.activeParticipants = selected.conv.Participants
		m.activeMsgCount = selected.conv.MessageCount
		m.messages = nil
		m.oldestCursor = 0
		m.allLoaded = false
		m.loading = true
		m.viewport.Height = calcViewportHeight(m.height, len(m.activeParticipants))
		return m, m.fetchMessagesCmd(selected.conv.ChatID, 0, false)

	case "s":
		if m.convList.FilterState() == list.Unfiltered {
			m.state = viewSearch
			m.searchInput.Focus()
			m.searchInput.SetValue("")
			return m, textinput.Blink
		}

	case "q":
		if m.convList.FilterState() == list.Unfiltered {
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.convList, cmd = m.convList.Update(msg)
	return m, cmd
}

func (m model) updateMessageView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
		m.state = viewConversations
		m.messages = nil
		m.exportStatus = ""
		return m, nil
	case "t":
		m.viewport.GotoTop()
		return m, nil
	case "b":
		m.viewport.GotoBottom()
		return m, nil
	case "e":
		if !m.exporting {
			m.exporting = true
			m.exportStatus = "Exporting..."
			return m, m.exportCmd()
		}
		return m, nil
	case "a":
		m.state = viewAttachments
		m.attachmentList.Title = "Loading attachments..."
		return m, m.fetchAttachmentsCmd(m.activeChatID)
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)

	if m.viewport.AtTop() && !m.allLoaded && !m.loading {
		m.loading = true
		loadCmd := m.fetchMessagesCmd(m.activeChatID, m.oldestCursor, true)
		return m, tea.Batch(cmd, loadCmd)
	}

	return m, cmd
}

func (m model) updateSearchView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.searchInput.Focused() {
		switch msg.String() {
		case "enter":
			query := strings.TrimSpace(m.searchInput.Value())
			if query == "" {
				return m, nil
			}
			m.searchInput.Blur()
			m.searching = true
			m.searchResults.Title = "Searching..."
			return m, m.searchCmd(query)
		case "esc":
			m.state = viewConversations
			m.searchInput.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		return m, cmd
	}

	// Results browsing mode
	switch msg.String() {
	case "esc":
		m.state = viewConversations
		return m, nil
	case "s":
		m.searchInput.Focus()
		m.searchInput.SetValue("")
		return m, textinput.Blink
	case "enter":
		selected, ok := m.searchResults.SelectedItem().(searchItem)
		if !ok {
			return m, nil
		}
		// Open the conversation containing this message
		m.state = viewMessages
		m.activeChatID = selected.result.ChatID
		m.activeChatTitle = m.contacts.ResolveName(selected.result.ChatName)
		m.activeParticipants = nil
		m.activeMsgCount = 0
		// Find participants from loaded conversations
		for _, conv := range m.convItems {
			if conv.ChatID == selected.result.ChatID {
				m.activeParticipants = conv.Participants
				m.activeMsgCount = conv.MessageCount
				// Re-resolve the title using the convItem logic
				ci := convItem{conv: conv, contacts: m.contacts}
				m.activeChatTitle = ci.Title()
				break
			}
		}
		m.messages = nil
		m.oldestCursor = 0
		m.allLoaded = false
		m.loading = true
		m.viewport.Height = calcViewportHeight(m.height, len(m.activeParticipants))
		return m, m.fetchMessagesCmd(selected.result.ChatID, 0, false)
	}

	var cmd tea.Cmd
	m.searchResults, cmd = m.searchResults.Update(msg)
	return m, cmd
}

func (m model) updateAttachmentView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
		if m.attachmentList.FilterState() == list.Filtering {
			m.attachmentList.ResetFilter()
			return m, nil
		}
		m.state = viewMessages
		return m, nil
	case "enter":
		if m.attachmentList.FilterState() == list.Filtering {
			var cmd tea.Cmd
			m.attachmentList, cmd = m.attachmentList.Update(msg)
			return m, cmd
		}
		selected, ok := m.attachmentList.SelectedItem().(attachmentItem)
		if !ok {
			return m, nil
		}
		return m, m.openAttachmentCmd(selected.attachment.FilePath)
	}

	var cmd tea.Cmd
	m.attachmentList, cmd = m.attachmentList.Update(msg)
	return m, cmd
}

func (m model) fetchAttachmentsCmd(chatID int) tea.Cmd {
	return func() tea.Msg {
		attachments, err := m.store.FetchChatAttachments(chatID)
		return attachmentsLoadedMsg{attachments: attachments, err: err}
	}
}

func (m model) openAttachmentCmd(path string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("open", path)
		err := cmd.Start()
		return attachmentOpenedMsg{err: err}
	}
}

func (m model) fetchMessagesCmd(chatID int, cursor int, prepend bool) tea.Cmd {
	return func() tea.Msg {
		msgs, err := m.store.FetchMessages(chatID, cursor, messagesPageSize)
		return messagesLoadedMsg{
			messages: msgs,
			chatID:   chatID,
			prepend:  prepend,
			err:      err,
		}
	}
}

func (m model) exportCmd() tea.Cmd {
	chatID := m.activeChatID
	participants := m.activeParticipants
	title := m.activeChatTitle
	return func() tea.Msg {
		path, err := exportCSV(m.store, m.contacts, chatID, participants, title)
		return exportDoneMsg{path: path, err: err}
	}
}

func (m model) searchCmd(term string) tea.Cmd {
	return func() tea.Msg {
		results, err := m.store.SearchMessages(term, 100)
		return searchResultsMsg{results: results, term: term, err: err}
	}
}

func calcViewportHeight(totalHeight int, participantCount int) int {
	headerLines := 2 + participantCount // title + count + participants + border
	footerH := 1
	h := totalHeight - headerLines - footerH - 4
	if h < 1 {
		h = 1
	}
	return h
}

func (m model) buildMessageHeader() string {
	var lines []string
	lines = append(lines, fmt.Sprintf(" %s", m.activeChatTitle))

	// Show contact details for each participant
	for _, handle := range m.activeParticipants {
		c := m.contacts.Resolve(handle)
		if c != nil {
			var details []string
			for _, p := range c.Phones {
				details = append(details, p)
			}
			for _, e := range c.Emails {
				details = append(details, e)
			}
			if len(details) > 0 {
				lines = append(lines, fmt.Sprintf(" %s: %s", c.Name, strings.Join(details, ", ")))
			}
		} else {
			lines = append(lines, fmt.Sprintf(" %s", handle))
		}
	}

	countInfo := fmt.Sprintf(" %d loaded / %d total", len(m.messages), m.activeMsgCount)
	lines = append(lines, countInfo)

	return strings.Join(lines, "\n")
}

func (m model) renderMessages() string {
	var sb strings.Builder
	var lastDate string

	if m.allLoaded {
		sb.WriteString(dateSepStyle.Width(m.viewport.Width).Render("— Beginning of conversation —"))
		sb.WriteString("\n\n")
	} else if m.loading {
		sb.WriteString(dateSepStyle.Width(m.viewport.Width).Render("Loading older messages..."))
		sb.WriteString("\n\n")
	}

	for _, msg := range m.messages {
		dateStr := msg.Date.Format("Monday, January 2, 2006")
		if dateStr != lastDate {
			lastDate = dateStr
			sb.WriteString("\n")
			sb.WriteString(dateSepStyle.Width(m.viewport.Width).Render(fmt.Sprintf("— %s —", dateStr)))
			sb.WriteString("\n\n")
		}

		ts := timestampStyle.Render(formatMessageTime(msg.Date))

		var sender string
		var styledSender string
		if msg.IsFromMe {
			sender = "Me"
			styledSender = senderStyle.Copy().Inherit(fromMeStyle).Render(truncate(sender, senderWidth))
		} else {
			sender = m.contacts.ResolveName(msg.Sender)
			if sender == "" {
				sender = "Unknown"
			}
			styledSender = senderStyle.Copy().Inherit(fromThemStyle).Render(truncate(sender, senderWidth))
		}

		text := msg.Text
		if len(msg.Attachments) > 0 {
			label := formatAttachments(msg.Attachments)
			if text == "" {
				text = attachmentStyle.Render(label)
			} else {
				text = text + "  " + attachmentStyle.Render(label)
			}
		} else if text == "" {
			text = attachmentStyle.Render("[attachment]")
		}

		sb.WriteString(fmt.Sprintf("%s  %s  %s\n", ts, styledSender, text))
	}

	return sb.String()
}

func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf("\n  Error: %v\n\n  Press any key to exit.\n", m.err)
	}

	switch m.state {
	case viewConversations:
		help := helpStyle.Render("  s: search all messages")
		return appStyle.Render(m.convList.View() + "\n" + help)

	case viewMessages:
		headerText := m.buildMessageHeader()
		header := headerStyle.Width(m.viewport.Width).Render(headerText)
		footerText := fmt.Sprintf(" %.0f%%  |  esc: back  |  e: export CSV  |  a: attachments  |  t/b: top/bottom",
			m.viewport.ScrollPercent()*100)
		if m.exportStatus != "" {
			footerText += "  |  " + m.exportStatus
		}
		footer := statusBarStyle.Render(footerText)
		return appStyle.Render(
			lipgloss.JoinVertical(lipgloss.Left, header, m.viewport.View(), footer),
		)

	case viewAttachments:
		help := helpStyle.Render("  enter: open  |  /: filter  |  esc: back")
		return appStyle.Render(m.attachmentList.View() + "\n" + help)

	case viewSearch:
		var sections []string

		inputLabel := searchInputStyle.Render(" Search ")
		inputRow := lipgloss.JoinHorizontal(lipgloss.Center, inputLabel, " ", m.searchInput.View())
		sections = append(sections, inputRow)

		if m.searching {
			sections = append(sections, "\n"+searchCountStyle.Render("  Searching..."))
		}

		sections = append(sections, m.searchResults.View())

		help := helpStyle.Render("  enter: open conversation  |  s: new search  |  esc: back")
		sections = append(sections, help)

		return appStyle.Render(lipgloss.JoinVertical(lipgloss.Left, sections...))
	}

	return ""
}
