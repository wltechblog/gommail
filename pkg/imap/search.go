package imap

// Code in this file was mechanically split from worker.go.

import (
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/wltechblog/gommail/internal/email"
)

// handleSearchMessages handles the search messages command
func (w *IMAPWorker) handleSearchMessages(cmd *IMAPCommand) *IMAPResponse {
	w.logger.Debug("Handling search messages command")

	if w.commandClient == nil {
		return NewResponse(cmd.ID, false, nil, fmt.Errorf("not connected"))
	}

	folderName, hasFolder := cmd.GetString(ParamFolderName)
	criteria, ok := cmd.GetSearchCriteria(ParamCriteria)
	if !ok || criteria == nil {
		searchTerm, _ := cmd.GetString(ParamSearchTerm)
		criteria = &email.SearchCriteria{Content: searchTerm}
	}

	desc := w.describeSearchCriteria(criteria)

	if hasFolder && folderName != "" && !strings.EqualFold(folderName, "All Folders") {
		w.logger.Info("Search request folder=%s criteria=%s max=%d server=%v", folderName, desc, criteria.MaxResults, criteria.SearchServer)

		messages, err := w.searchMessagesInFolder(folderName, criteria, criteria.MaxResults)
		if err != nil {
			w.logger.Error("Failed to search folder %s: %v", folderName, err)
			w.incrementErrorCount()
			return NewResponse(cmd.ID, false, nil, err)
		}

		return NewResponse(cmd.ID, true, map[string]interface{}{
			DataMessages: messages,
		}, nil)
	}

	w.logger.Info("Search request all folders criteria=%s max=%d server=%v", desc, criteria.MaxResults, criteria.SearchServer)

	messages, err := w.searchMessagesAcrossFolders(criteria)
	if err != nil {
		w.logger.Error("Failed to search across folders: %v", err)
		w.incrementErrorCount()
		return NewResponse(cmd.ID, false, nil, err)
	}

	return NewResponse(cmd.ID, true, map[string]interface{}{
		DataMessages: messages,
	}, nil)
}

func (w *IMAPWorker) searchMessagesAcrossFolders(criteria *email.SearchCriteria) ([]email.Message, error) {
	folders, err := w.getSearchableFolderNames()
	if err != nil {
		return nil, err
	}

	if len(folders) == 0 {
		w.logger.Warn("Search aborted because no selectable folders were found")
		return []email.Message{}, nil
	}

	w.logger.Info("Search spanning %d folders: %s", len(folders), strings.Join(folders, ","))

	maxTotal := criteria.MaxResults
	remaining := maxTotal
	var allMessages []email.Message

	for _, folder := range folders {
		if maxTotal > 0 && remaining <= 0 {
			break
		}

		limit := 0
		if maxTotal > 0 {
			limit = remaining
		}

		w.logger.Debug("Searching folder %s with limit %d", folder, limit)

		folderMessages, err := w.searchMessagesInFolder(folder, criteria, limit)
		if err != nil {
			return nil, fmt.Errorf("failed to search folder %s: %w", folder, err)
		}

		w.logger.Info("Folder %s produced %d messages", folder, len(folderMessages))

		allMessages = append(allMessages, folderMessages...)
		if maxTotal > 0 {
			remaining -= len(folderMessages)
			if remaining <= 0 {
				break
			}
		}
	}

	w.logger.Info("Aggregated %d messages across folders", len(allMessages))

	return allMessages, nil
}

func (w *IMAPWorker) searchMessagesInFolder(folderName string, criteria *email.SearchCriteria, maxResults int) ([]email.Message, error) {
	if folderName == "" {
		return nil, fmt.Errorf("folder name not provided")
	}

	if w.selectedFolder != folderName {
		if err := w.ensureSelected(folderName); err != nil {
			return nil, err
		}
		w.logger.Debug("Selected folder %s for search", folderName)
	}

	searchCriteria := w.buildIMAPSearchCriteria(criteria)
	searchCmd := w.commandClient.UIDSearch(searchCriteria, nil)
	searchData, err := searchCmd.Wait()
	if err != nil {
		return nil, fmt.Errorf("failed to search messages: %w", err)
	}

	uids := searchData.AllUIDs()
	w.logger.Debug("Search in folder %s found %d messages", folderName, len(uids))
	if len(uids) == 0 {
		w.logger.Info("Folder %s returned no matching UIDs", folderName)
		return []email.Message{}, nil
	}

	limit := maxResults
	if limit > 0 && len(uids) > limit {
		w.logger.Info("Folder %s limiting results from %d to %d based on MaxResults", folderName, len(uids), limit)
		uids = uids[len(uids)-limit:]
	}

	uidSet := imap.UIDSet{}
	if len(uids) > 0 {
		uidSet.AddRange(uids[0], uids[len(uids)-1])
	}

	fetchCmd := w.commandClient.Fetch(uidSet, &imap.FetchOptions{
		Envelope:     true,
		Flags:        true,
		UID:          true,
		RFC822Size:   true,
		InternalDate: true,
	})

	messages := make([]email.Message, 0, len(uids))
	for {
		msg := fetchCmd.Next()
		if msg == nil {
			break
		}
		messages = append(messages, w.convertFetchDataToMessage(msg))
	}

	if err := fetchCmd.Close(); err != nil {
		w.logger.Warn("Error closing fetch command: %v", err)
	}

	w.logger.Info("Folder %s returning %d hydrated messages", folderName, len(messages))

	return messages, nil
}

func (w *IMAPWorker) buildIMAPSearchCriteria(criteria *email.SearchCriteria) *imap.SearchCriteria {
	result := &imap.SearchCriteria{}
	if criteria == nil {
		return result
	}

	if criteria.From != "" {
		result.Header = append(result.Header, imap.SearchCriteriaHeaderField{Key: "From", Value: criteria.From})
	}
	if criteria.To != "" {
		result.Header = append(result.Header, imap.SearchCriteriaHeaderField{Key: "To", Value: criteria.To})
	}
	if criteria.Subject != "" {
		result.Header = append(result.Header, imap.SearchCriteriaHeaderField{Key: "Subject", Value: criteria.Subject})
	}
	if criteria.Content != "" {
		result.Body = []string{criteria.Content}
	}
	if criteria.DateFrom != nil {
		result.Since = *criteria.DateFrom
	}
	if criteria.DateTo != nil {
		result.Before = *criteria.DateTo
	}
	if criteria.UnreadOnly {
		result.NotFlag = append(result.NotFlag, imap.FlagSeen)
	}
	if criteria.MessageSize != nil {
		result.Larger = *criteria.MessageSize
	}
	for _, keyword := range criteria.Keywords {
		if keyword != "" {
			result.Flag = append(result.Flag, imap.Flag(keyword))
		}
	}

	return result
}

func (w *IMAPWorker) describeSearchCriteria(criteria *email.SearchCriteria) string {
	if criteria == nil {
		return "none"
	}

	parts := []string{}

	if criteria.From != "" {
		parts = append(parts, fmt.Sprintf("from=%s", criteria.From))
	}
	if criteria.To != "" {
		parts = append(parts, fmt.Sprintf("to=%s", criteria.To))
	}
	if criteria.Subject != "" {
		parts = append(parts, fmt.Sprintf("subject=%s", criteria.Subject))
	}
	if criteria.Content != "" {
		parts = append(parts, fmt.Sprintf("body~=%s", criteria.Content))
	}
	if criteria.DateFrom != nil {
		parts = append(parts, fmt.Sprintf("since=%s", criteria.DateFrom.Format(time.RFC3339)))
	}
	if criteria.DateTo != nil {
		parts = append(parts, fmt.Sprintf("before=%s", criteria.DateTo.Format(time.RFC3339)))
	}
	if criteria.HasAttachments {
		parts = append(parts, "attachments=true")
	}
	if criteria.UnreadOnly {
		parts = append(parts, "unread=true")
	}
	if criteria.MessageSize != nil {
		parts = append(parts, fmt.Sprintf("minSize=%d", *criteria.MessageSize))
	}
	if len(criteria.Keywords) > 0 {
		parts = append(parts, fmt.Sprintf("keywords=%d", len(criteria.Keywords)))
	}
	if criteria.CaseSensitive {
		parts = append(parts, "caseSensitive=true")
	}
	if criteria.UseRegex {
		parts = append(parts, "regex=true")
	}
	if criteria.SearchServer {
		parts = append(parts, "serverSearch=true")
	}

	if len(parts) == 0 {
		return "empty"
	}

	return strings.Join(parts, ",")
}

func (w *IMAPWorker) getSearchableFolderNames() ([]string, error) {
	if folders, found, err := w.getCachedSubscribedFolders(); err == nil && found {
		w.logger.Debug("Using %d cached subscribed folders for search", len(folders))
		names := w.filterSelectableFolders(folders)
		if len(names) > 0 {
			return names, nil
		}
		w.logger.Debug("Cached folders produced no selectable entries, falling back to server list")
	}

	if w.commandClient == nil {
		return nil, fmt.Errorf("not connected")
	}

	w.logger.Info("Listing subscribed folders from server for search")
	listCmd := w.commandClient.List("", "*", &imap.ListOptions{SelectSubscribed: true})
	mailboxes, err := listCmd.Collect()
	if err != nil {
		return nil, fmt.Errorf("failed to list folders: %w", err)
	}

	folders := make([]email.Folder, 0, len(mailboxes))
	names := make([]string, 0, len(mailboxes))
	for _, mb := range mailboxes {
		if mb == nil {
			continue
		}
		attrs := w.convertAttributes(mb.Attrs)
		delimiter := string(mb.Delim)
		if delimiter == "" {
			delimiter = "/"
		}
		folder := email.Folder{
			Name:       mb.Mailbox,
			Path:       mb.Mailbox,
			Delimiter:  delimiter,
			Attributes: attrs,
			Subscribed: true,
		}
		folders = append(folders, folder)
		if !w.folderHasNoSelect(attrs) {
			names = append(names, folder.Name)
		}
	}

	if err := w.cacheSubscribedFolders(folders); err != nil {
		w.logger.Warn("Failed to cache subscribed folders: %v", err)
	} else {
		w.logger.Debug("Cached %d subscribed folders for future searches", len(folders))
	}

	return names, nil
}

func (w *IMAPWorker) filterSelectableFolders(folders []email.Folder) []string {
	var names []string
	for _, folder := range folders {
		if folder.Name == "" {
			continue
		}
		if w.folderHasNoSelect(folder.Attributes) {
			continue
		}
		names = append(names, folder.Name)
	}
	return names
}

func (w *IMAPWorker) folderHasNoSelect(attrs []string) bool {
	for _, attr := range attrs {
		if strings.EqualFold(attr, string(imap.MailboxAttrNoSelect)) {
			return true
		}
	}
	return false
}
