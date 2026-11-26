package workmanagement

import (
	"context"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkdrive "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
)

// RegisterFeishuEvents installs the Bitable record and field change handlers
// on the shared Feishu long-connection dispatcher. The handlers only persist
// the Event Inbox row; the worker projects it afterwards, keeping the
// acknowledgement inside Feishu's three-second window.
func (s *Service) RegisterFeishuEvents(d *dispatcher.EventDispatcher) {
	if s == nil || d == nil {
		return
	}
	d.OnP2FileBitableRecordChangedV1(func(ctx context.Context, event *larkdrive.P2FileBitableRecordChangedV1) error {
		parsed, ok := recordChangedFromFeishu(event)
		if !ok {
			return nil
		}
		return s.AcceptRecordChanged(ctx, parsed)
	})
	d.OnP2FileBitableFieldChangedV1(func(ctx context.Context, event *larkdrive.P2FileBitableFieldChangedV1) error {
		parsed, ok := fieldChangedFromFeishu(event)
		if !ok {
			return nil
		}
		return s.AcceptFieldChanged(ctx, parsed)
	})
}

func recordChangedFromFeishu(event *larkdrive.P2FileBitableRecordChangedV1) (RecordChangedEvent, bool) {
	if event == nil || event.EventV2Base == nil || event.EventV2Base.Header == nil || event.Event == nil {
		return RecordChangedEvent{}, false
	}
	out := RecordChangedEvent{
		EventID:   event.EventV2Base.Header.EventID,
		FileToken: stringValue(event.Event.FileToken),
		TableID:   stringValue(event.Event.TableId),
	}
	for _, action := range event.Event.ActionList {
		if action == nil {
			continue
		}
		out.Actions = append(out.Actions, RecordAction{
			RecordID: stringValue(action.RecordId),
			Action:   stringValue(action.Action),
			Before:   eventFields(action.BeforeValue),
			After:    eventFields(action.AfterValue),
		})
	}
	return out, out.EventID != "" && out.FileToken != "" && out.TableID != ""
}

func fieldChangedFromFeishu(event *larkdrive.P2FileBitableFieldChangedV1) (FieldChangedEvent, bool) {
	if event == nil || event.EventV2Base == nil || event.EventV2Base.Header == nil || event.Event == nil {
		return FieldChangedEvent{}, false
	}
	out := FieldChangedEvent{
		EventID:   event.EventV2Base.Header.EventID,
		FileToken: stringValue(event.Event.FileToken),
		TableID:   stringValue(event.Event.TableId),
	}
	return out, out.EventID != "" && out.FileToken != "" && out.TableID != ""
}

func eventFields(fields []*larkdrive.BitableTableRecordActionField) []ActionField {
	if len(fields) == 0 {
		return nil
	}
	out := make([]ActionField, 0, len(fields))
	for _, field := range fields {
		if field == nil {
			continue
		}
		out = append(out, ActionField{
			FieldID:    stringValue(field.FieldId),
			FieldValue: stringValue(field.FieldValue),
		})
	}
	return out
}
