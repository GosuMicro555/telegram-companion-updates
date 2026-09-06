package wails

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	coreanalytics "telegram-companion/internal/analytics"
	"telegram-companion/internal/domain"
)

var livePageSizes = map[int]struct{}{50: {}, 100: {}, 200: {}, 500: {}, 1000: {}}

var liveSortColumns = map[string]domain.LiveDeliverySortColumn{
	"triggeredAt":          domain.LiveDeliverySortTriggeredAt,
	"sourceMessage":        domain.LiveDeliverySortSourceMessage,
	"message":              domain.LiveDeliverySortSourceMessage,
	"triggerSnapshot":      domain.LiveDeliverySortTriggerSnapshot,
	"trigger":              domain.LiveDeliverySortTriggerSnapshot,
	"deliveryType":         domain.LiveDeliverySortDeliveryType,
	"type":                 domain.LiveDeliverySortDeliveryType,
	"accountTitleSnapshot": domain.LiveDeliverySortAccountTitle,
	"account":              domain.LiveDeliverySortAccountTitle,
	"finalStatus":          domain.LiveDeliverySortFinalStatus,
	"status":               domain.LiveDeliverySortFinalStatus,
	"finalizedAt":          domain.LiveDeliverySortFinalizedAt,
}

type LiveDeliveryQueryDTO struct {
	From          string `json:"from"`
	To            string `json:"to"`
	Page          int    `json:"page"`
	PageSize      int    `json:"pageSize"`
	SortBy        string `json:"sortBy"`
	SortDirection string `json:"sortDirection"`
}

type LiveDeliveryExportRequestDTO struct {
	From          string   `json:"from"`
	To            string   `json:"to"`
	SortBy        string   `json:"sortBy"`
	SortDirection string   `json:"sortDirection"`
	Columns       []string `json:"columns"`
	Locale        string   `json:"locale"`
	TimeZone      string   `json:"timeZone"`
}

type LiveDeliveryPageDTO struct {
	Rows          []LiveDeliveryRowDTO `json:"rows"`
	Total         int                  `json:"total"`
	DatabaseBytes int64                `json:"databaseBytes"`
	RefreshedAt   string               `json:"refreshedAt"`
}

type LiveDeliveryRowDTO struct {
	ID                   string  `json:"id"`
	SourceMessage        string  `json:"sourceMessage"`
	TriggerCanonicalID   *string `json:"triggerCanonicalID"`
	TriggerSnapshot      string  `json:"triggerSnapshot"`
	TriggeredAt          string  `json:"triggeredAt"`
	DeliveryType         string  `json:"deliveryType"`
	AccountTitleSnapshot string  `json:"accountTitleSnapshot"`
	FinalStatus          string  `json:"finalStatus"`
	ErrorCode            string  `json:"errorCode"`
	FinalizedAt          string  `json:"finalizedAt"`
}

func (b *Bindings) GetLiveDeliveryStatistics(input LiveDeliveryQueryDTO) (LiveDeliveryPageDTO, error) {
	if err := b.runtimeError(); err != nil {
		return LiveDeliveryPageDTO{}, err
	}
	provider, ok := b.settings.(domain.LiveDeliveryStatisticsProvider)
	if !ok {
		return LiveDeliveryPageDTO{}, errors.New("live delivery statistics are unavailable")
	}
	query, err := liveDeliveryQuery(input)
	if err != nil {
		return LiveDeliveryPageDTO{}, err
	}
	ctx, cancel := context.WithTimeout(b.rootContext(), 30*time.Second)
	defer cancel()
	page, err := provider.LiveDeliveryStatistics(ctx, query)
	if err != nil {
		return LiveDeliveryPageDTO{}, err
	}
	if b.canonicalAnalytics != nil {
		settings, err := b.settings.Load(ctx)
		if err != nil {
			return LiveDeliveryPageDTO{}, err
		}
		canonicals, err := b.listCanonicalKeywords(ctx)
		if err != nil {
			return LiveDeliveryPageDTO{}, err
		}
		rebindLiveDeliveryCanonicalIDs(page.Rows, canonicalOwnedRows(canonicals, settings))
	}
	return liveDeliveryPageDTO(page, time.Now().UTC()), nil
}

func (b *Bindings) ExportLiveDeliveryStatistics(input LiveDeliveryExportRequestDTO) (string, error) {
	if err := b.runtimeError(); err != nil {
		return "", err
	}
	provider, ok := b.settings.(domain.LiveDeliveryStatisticsProvider)
	if !ok {
		return "", errors.New("live delivery statistics are unavailable")
	}
	if b.liveStatsExportSelector == nil {
		return "", errors.New("live delivery statistics export is unavailable")
	}
	query, err := liveDeliveryQuery(LiveDeliveryQueryDTO{
		From: input.From, To: input.To, Page: 1, PageSize: 1000,
		SortBy: input.SortBy, SortDirection: input.SortDirection,
	})
	if err != nil {
		return "", err
	}
	columns, err := liveDeliveryExportColumns(input.Columns)
	if err != nil {
		return "", err
	}
	location, err := liveDeliveryExportLocation(input.TimeZone)
	if err != nil {
		return "", err
	}
	locale := normalizeDesktopLocale(input.Locale)
	defaultName := "live-statistics-" + time.Now().In(location).Format("2006-01-02") + ".csv"
	ctx, cancel := context.WithTimeout(b.rootContext(), 5*time.Minute)
	defer cancel()
	path, err := b.liveStatsExportSelector.SelectLiveStatisticsExportFile(ctx, defaultName, locale)
	if err != nil || strings.TrimSpace(path) == "" {
		return "", err
	}
	if filepath.Ext(path) == "" {
		path += ".csv"
	}
	file, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create live statistics export: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	if _, err := file.WriteString("\ufeff"); err != nil {
		return "", fmt.Errorf("write live statistics export BOM: %w", err)
	}
	w := csv.NewWriter(file)
	if err := w.Write(liveDeliveryExportHeaders(columns, locale)); err != nil {
		return "", fmt.Errorf("write live statistics export header: %w", err)
	}
	for {
		page, err := provider.LiveDeliveryStatistics(ctx, query)
		if err != nil {
			return "", err
		}
		for _, row := range page.Rows {
			if err := w.Write(liveDeliveryExportRow(row, columns, locale, location)); err != nil {
				return "", fmt.Errorf("write live statistics export row: %w", err)
			}
		}
		query.Offset += query.Limit
		if query.Offset >= page.Total {
			break
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", fmt.Errorf("flush live statistics export: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close live statistics export: %w", err)
	}
	closed = true
	return path, nil
}

var liveExportColumnSet = map[string]struct{}{
	"sourceMessage": {}, "trigger": {}, "type": {}, "date": {}, "time": {}, "account": {}, "status": {},
}

func liveDeliveryExportColumns(input []string) ([]string, error) {
	if len(input) == 0 {
		return nil, errors.New("live delivery statistics export requires at least one column")
	}
	result := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, column := range input {
		if _, ok := liveExportColumnSet[column]; !ok {
			return nil, errors.New("live delivery statistics export column is unsupported")
		}
		if _, duplicate := seen[column]; duplicate {
			continue
		}
		seen[column] = struct{}{}
		result = append(result, column)
	}
	return result, nil
}

func liveDeliveryExportLocation(value string) (*time.Location, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "Europe/Moscow"
	}
	location, err := time.LoadLocation(value)
	if err != nil {
		return nil, fmt.Errorf("load live statistics export time zone: %w", err)
	}
	return location, nil
}

func liveDeliveryExportHeaders(columns []string, locale string) []string {
	ru := locale != "en"
	headings := map[string][2]string{
		"sourceMessage": {"Исходное сообщение", "Source message"},
		"trigger":       {"Триггер", "Trigger"},
		"type":          {"Тип", "Type"},
		"date":          {"Дата", "Date"},
		"time":          {"Время", "Time"},
		"account":       {"Telegram аккаунт", "Telegram account"},
		"status":        {"Статус", "Status"},
	}
	result := make([]string, 0, len(columns))
	for _, column := range columns {
		if ru {
			result = append(result, headings[column][0])
		} else {
			result = append(result, headings[column][1])
		}
	}
	return result
}

func liveDeliveryExportRow(row domain.LiveDeliveryRow, columns []string, locale string, location *time.Location) []string {
	ru := locale != "en"
	triggeredAt := row.TriggeredAt.In(location)
	values := map[string]string{
		"sourceMessage": row.SourceMessage,
		"trigger":       row.TriggerSnapshot,
		"type":          liveDeliveryExportType(row, ru),
		"date":          triggeredAt.Format("02.01.2006"),
		"time":          triggeredAt.Format("15:04:05"),
		"account":       row.AccountTitleSnapshot,
		"status":        liveDeliveryExportStatus(row, ru),
	}
	result := make([]string, 0, len(columns))
	for _, column := range columns {
		result = append(result, values[column])
	}
	return result
}

func liveDeliveryExportType(row domain.LiveDeliveryRow, ru bool) string {
	if row.DeliveryType == nil {
		return "-"
	}
	if string(*row.DeliveryType) == "private_message" {
		if ru {
			return "ЛС"
		}
		return "DM"
	}
	if ru {
		return "Ответ"
	}
	return "Reply"
}

func liveDeliveryExportStatus(row domain.LiveDeliveryRow, ru bool) string {
	if row.ErrorCode == "private_message_closed" {
		if ru {
			return "Закрыта личка"
		}
		return "DM closed"
	}
	if row.FinalStatus != nil && *row.FinalStatus == "successful" {
		if ru {
			return "Успешно"
		}
		return "Delivered"
	}
	if row.FinalStatus == nil || *row.FinalStatus == "" {
		return "-"
	}
	if ru {
		return "Не доставлено"
	}
	return "Not delivered"
}

func rebindLiveDeliveryCanonicalIDs(rows []domain.LiveDeliveryRow, canonicals []coreanalytics.CanonicalKeyword) {
	currentByValue := make(map[string]domain.ID)
	for _, canonical := range canonicals {
		values := []string{canonical.Canonical}
		for _, form := range canonical.Forms {
			values = append(values, form.Value)
		}
		for _, value := range values {
			key := normalizeKeyword(value)
			if key != "" {
				if _, exists := currentByValue[key]; !exists {
					currentByValue[key] = domain.ID(canonical.ID)
				}
			}
		}
	}
	for index := range rows {
		rows[index].TriggerCanonicalID = nil
		if id, exists := currentByValue[normalizeKeyword(rows[index].TriggerSnapshot)]; exists {
			current := id
			rows[index].TriggerCanonicalID = &current
		}
	}
}

func liveDeliveryQuery(input LiveDeliveryQueryDTO) (domain.LiveDeliveryQuery, error) {
	from, err := parseLiveDeliveryTime(input.From)
	if err != nil {
		return domain.LiveDeliveryQuery{}, err
	}
	to, err := parseLiveDeliveryTime(input.To)
	if err != nil {
		return domain.LiveDeliveryQuery{}, err
	}
	if !from.IsZero() && !to.IsZero() && !to.After(from) {
		return domain.LiveDeliveryQuery{}, errors.New("live delivery statistics end must be after start")
	}
	page := input.Page
	if page == 0 {
		page = 1
	}
	if page < 1 {
		return domain.LiveDeliveryQuery{}, errors.New("live delivery statistics page must be positive")
	}
	pageSize := input.PageSize
	if pageSize == 0 {
		pageSize = 50
	}
	if _, ok := livePageSizes[pageSize]; !ok {
		return domain.LiveDeliveryQuery{}, errors.New("live delivery statistics page size must be one of 50, 100, 200, 500, or 1000")
	}
	sortBy := domain.LiveDeliverySortTriggeredAt
	if value := strings.TrimSpace(input.SortBy); value != "" {
		var ok bool
		sortBy, ok = liveSortColumns[value]
		if !ok {
			return domain.LiveDeliveryQuery{}, errors.New("live delivery statistics sort column is unsupported")
		}
	}
	direction := domain.LiveDeliverySortDescending
	if value := strings.TrimSpace(input.SortDirection); value != "" {
		direction = domain.LiveDeliverySortDirection(value)
	}
	if direction != domain.LiveDeliverySortAscending && direction != domain.LiveDeliverySortDescending {
		return domain.LiveDeliveryQuery{}, errors.New("live delivery statistics sort direction is unsupported")
	}
	return domain.LiveDeliveryQuery{
		From: from, To: to, Limit: pageSize, Offset: (page - 1) * pageSize, SortBy: sortBy, SortDirection: direction,
	}, nil
}

func parseLiveDeliveryTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse live delivery time: %w", err)
	}
	return parsed.UTC(), nil
}

func liveDeliveryPageDTO(page domain.LiveDeliveryPage, refreshedAt time.Time) LiveDeliveryPageDTO {
	result := LiveDeliveryPageDTO{
		Rows: make([]LiveDeliveryRowDTO, 0, len(page.Rows)), Total: page.Total, DatabaseBytes: page.DatabaseBytes,
		RefreshedAt: refreshedAt.UTC().Format(time.RFC3339Nano),
	}
	for _, row := range page.Rows {
		result.Rows = append(result.Rows, liveDeliveryRowDTO(row))
	}
	return result
}

func liveDeliveryRowDTO(row domain.LiveDeliveryRow) LiveDeliveryRowDTO {
	result := LiveDeliveryRowDTO{
		ID: string(row.ID), SourceMessage: row.SourceMessage, TriggerSnapshot: row.TriggerSnapshot,
		TriggeredAt: row.TriggeredAt.UTC().Format(time.RFC3339Nano), AccountTitleSnapshot: row.AccountTitleSnapshot,
		ErrorCode: row.ErrorCode,
	}
	if row.TriggerCanonicalID != nil {
		value := string(*row.TriggerCanonicalID)
		result.TriggerCanonicalID = &value
	}
	if row.DeliveryType != nil {
		result.DeliveryType = string(*row.DeliveryType)
	}
	if row.FinalStatus != nil {
		result.FinalStatus = *row.FinalStatus
	}
	if row.FinalizedAt != nil {
		result.FinalizedAt = row.FinalizedAt.UTC().Format(time.RFC3339Nano)
	}
	return result
}
