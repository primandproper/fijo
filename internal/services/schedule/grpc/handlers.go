package schedulegrpc

import (
	"context"
	"time"

	"github.com/primandproper/fijo/internal/domain/schedule"
	"github.com/primandproper/fijo/internal/grpc/fijopb"

	"github.com/primandproper/platform-go/v14/audit"
	"github.com/primandproper/primitives-go/v2/database"
	platformerrors "github.com/primandproper/primitives-go/v2/errors"
	"github.com/primandproper/primitives-go/v2/filtering"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// GetScheduleSettings implements fijopb.ScheduleServiceServer.
func (s *Server) GetScheduleSettings(ctx context.Context, _ *fijopb.GetScheduleSettingsRequest) (*fijopb.GetScheduleSettingsResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	_, scope, err := s.caller(ctx, op, fijopb.ScheduleService_GetScheduleSettings_FullMethodName)
	if err != nil {
		return nil, err
	}

	settings, err := s.store.GetSettings(ctx, s.client.Reader(), scope)
	if err != nil {
		return nil, s.fail(err, op, "reading the studio's schedule settings")
	}

	return &fijopb.GetScheduleSettingsResponse{Settings: settingsToProto(settings)}, nil
}

// SetScheduleSettings implements fijopb.ScheduleServiceServer.
func (s *Server) SetScheduleSettings(ctx context.Context, request *fijopb.SetScheduleSettingsRequest) (*fijopb.SetScheduleSettingsResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	_, scope, err := s.caller(ctx, op, fijopb.ScheduleService_SetScheduleSettings_FullMethodName)
	if err != nil {
		return nil, err
	}

	if request.GetSettings() == nil {
		return nil, s.fail(ErrNilRequest, op, "writing the studio's schedule settings")
	}

	var written *schedule.Settings

	if err = s.client.WithTransaction(ctx, func(tx database.Tx) error {
		var writeErr error

		written, writeErr = s.store.SetSettings(ctx, tx, scope, &schedule.Settings{
			NoticeWindow: time.Duration(request.GetSettings().GetNoticeWindowMinutes()) * time.Minute,
			HorizonWeeks: int(request.GetSettings().GetHorizonWeeks()),
		})
		if writeErr != nil {
			return writeErr
		}

		return s.syncStudio(ctx, tx, scope)
	}); err != nil {
		return nil, s.fail(err, op, "writing the studio's schedule settings")
	}

	return &fijopb.SetScheduleSettingsResponse{Settings: settingsToProto(written)}, nil
}

// ListOfferings implements fijopb.ScheduleServiceServer.
func (s *Server) ListOfferings(ctx context.Context, request *fijopb.ListOfferingsRequest) (*fijopb.ListOfferingsResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	_, scope, err := s.caller(ctx, op, fijopb.ScheduleService_ListOfferings_FullMethodName)
	if err != nil {
		return nil, err
	}

	offerings, err := s.store.ListOfferings(ctx, s.client.Reader(), scope, request.GetIncludeArchived())
	if err != nil {
		return nil, s.fail(err, op, "listing offerings")
	}

	out := make([]*fijopb.Offering, 0, len(offerings))
	for _, offering := range offerings {
		out = append(out, offeringToProto(offering))
	}

	return &fijopb.ListOfferingsResponse{Offerings: out}, nil
}

// CreateOffering implements fijopb.ScheduleServiceServer.
func (s *Server) CreateOffering(ctx context.Context, request *fijopb.CreateOfferingRequest) (*fijopb.CreateOfferingResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	_, scope, err := s.caller(ctx, op, fijopb.ScheduleService_CreateOffering_FullMethodName)
	if err != nil {
		return nil, err
	}

	if request.GetOffering() == nil {
		return nil, s.fail(ErrNilRequest, op, "creating an offering")
	}

	var written *schedule.Offering

	if err = s.client.WithTransaction(ctx, func(tx database.Tx) error {
		var writeErr error

		written, writeErr = s.store.CreateOffering(ctx, tx, scope, offeringFromProto(request.GetOffering()))

		return writeErr
	}); err != nil {
		return nil, s.fail(err, op, "creating an offering")
	}

	return &fijopb.CreateOfferingResponse{Offering: offeringToProto(written)}, nil
}

// UpdateOffering implements fijopb.ScheduleServiceServer.
func (s *Server) UpdateOffering(ctx context.Context, request *fijopb.UpdateOfferingRequest) (*fijopb.UpdateOfferingResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	_, scope, err := s.caller(ctx, op, fijopb.ScheduleService_UpdateOffering_FullMethodName)
	if err != nil {
		return nil, err
	}

	if request.GetOffering() == nil {
		return nil, s.fail(ErrNilRequest, op, "updating an offering")
	}

	var written *schedule.Offering

	if err = s.client.WithTransaction(ctx, func(tx database.Tx) error {
		var writeErr error

		written, writeErr = s.store.UpdateOffering(ctx, tx, scope, offeringFromProto(request.GetOffering()))
		if writeErr != nil {
			return writeErr
		}

		// A new length changes when every lesson it is sold as ends.
		series, listErr := s.store.ListSeries(ctx, tx, scope, false)
		if listErr != nil {
			return listErr
		}

		for _, one := range series {
			if one.OfferingID != written.ID {
				continue
			}

			if syncErr := s.syncSeries(ctx, tx, scope, one.ID); syncErr != nil {
				return syncErr
			}
		}

		return nil
	}); err != nil {
		return nil, s.fail(err, op, "updating an offering")
	}

	return &fijopb.UpdateOfferingResponse{Offering: offeringToProto(written)}, nil
}

// ArchiveOffering implements fijopb.ScheduleServiceServer.
func (s *Server) ArchiveOffering(ctx context.Context, request *fijopb.ArchiveOfferingRequest) (*fijopb.ArchiveOfferingResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	_, scope, err := s.caller(ctx, op, fijopb.ScheduleService_ArchiveOffering_FullMethodName)
	if err != nil {
		return nil, err
	}

	if err = s.client.WithTransaction(ctx, func(tx database.Tx) error {
		return s.store.ArchiveOffering(ctx, tx, scope, request.GetId())
	}); err != nil {
		return nil, s.fail(err, op, "archiving an offering")
	}

	return &fijopb.ArchiveOfferingResponse{}, nil
}

// ListSeries implements fijopb.ScheduleServiceServer.
func (s *Server) ListSeries(ctx context.Context, request *fijopb.ListSeriesRequest) (*fijopb.ListSeriesResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	_, scope, err := s.caller(ctx, op, fijopb.ScheduleService_ListSeries_FullMethodName)
	if err != nil {
		return nil, err
	}

	series, err := s.store.ListSeries(ctx, s.client.Reader(), scope, request.GetIncludeArchived())
	if err != nil {
		return nil, s.fail(err, op, "listing series")
	}

	out := make([]*fijopb.Series, 0, len(series))
	for _, one := range series {
		out = append(out, seriesToProto(one))
	}

	return &fijopb.ListSeriesResponse{Series: out}, nil
}

// CreateSeries implements fijopb.ScheduleServiceServer.
func (s *Server) CreateSeries(ctx context.Context, request *fijopb.CreateSeriesRequest) (*fijopb.CreateSeriesResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	_, scope, err := s.caller(ctx, op, fijopb.ScheduleService_CreateSeries_FullMethodName)
	if err != nil {
		return nil, err
	}

	if request.GetSeries() == nil {
		return nil, s.fail(ErrNilRequest, op, "creating a series")
	}

	staged, err := seriesFromProto(request.GetSeries())
	if err != nil {
		return nil, s.fail(err, op, "creating a series")
	}

	var written *schedule.Series

	if err = s.client.WithTransaction(ctx, func(tx database.Tx) error {
		var writeErr error

		written, writeErr = s.store.CreateSeries(ctx, tx, scope, staged)
		if writeErr != nil {
			return writeErr
		}

		return s.syncSeries(ctx, tx, scope, written.ID)
	}); err != nil {
		return nil, s.fail(err, op, "creating a series")
	}

	return &fijopb.CreateSeriesResponse{Series: seriesToProto(written)}, nil
}

// UpdateSeries implements fijopb.ScheduleServiceServer.
func (s *Server) UpdateSeries(ctx context.Context, request *fijopb.UpdateSeriesRequest) (*fijopb.UpdateSeriesResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	_, scope, err := s.caller(ctx, op, fijopb.ScheduleService_UpdateSeries_FullMethodName)
	if err != nil {
		return nil, err
	}

	if request.GetSeries() == nil {
		return nil, s.fail(ErrNilRequest, op, "updating a series")
	}

	staged, err := seriesFromProto(request.GetSeries())
	if err != nil {
		return nil, s.fail(err, op, "updating a series")
	}

	var written *schedule.Series

	if err = s.client.WithTransaction(ctx, func(tx database.Tx) error {
		var writeErr error

		written, writeErr = s.store.UpdateSeries(ctx, tx, scope, staged)
		if writeErr != nil {
			return writeErr
		}

		return s.syncSeries(ctx, tx, scope, written.ID)
	}); err != nil {
		return nil, s.fail(err, op, "updating a series")
	}

	return &fijopb.UpdateSeriesResponse{Series: seriesToProto(written)}, nil
}

// EndSeries implements fijopb.ScheduleServiceServer.
func (s *Server) EndSeries(ctx context.Context, request *fijopb.EndSeriesRequest) (*fijopb.EndSeriesResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	caller, scope, err := s.caller(ctx, op, fijopb.ScheduleService_EndSeries_FullMethodName)
	if err != nil {
		return nil, err
	}

	endsOn, err := parseDate(request.GetEndsOn())
	if err != nil {
		return nil, s.fail(err, op, "ending a series")
	}

	ended, err := s.commands.EndSeries(ctx, scope, caller.UserID(), request.GetId(), endsOn, request.GetReason())
	if err != nil {
		return nil, s.fail(err, op, "ending a series")
	}

	return &fijopb.EndSeriesResponse{Series: seriesToProto(ended)}, nil
}

// ListOccurrences implements fijopb.ScheduleServiceServer.
func (s *Server) ListOccurrences(ctx context.Context, request *fijopb.ListOccurrencesRequest) (*fijopb.ListOccurrencesResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	_, scope, err := s.caller(ctx, op, fijopb.ScheduleService_ListOccurrences_FullMethodName)
	if err != nil {
		return nil, err
	}

	if request.GetFrom() == nil || request.GetUntil() == nil {
		return nil, s.fail(platformerrors.Wrap(platformerrors.ErrUnrecognizedInputValue, "both ends of the window are required"), op, "listing occurrences")
	}

	occurrences, err := s.store.ListOccurrencesInWindow(ctx, s.client.Reader(), scope, request.GetFrom().AsTime(), request.GetUntil().AsTime())
	if err != nil {
		return nil, s.fail(err, op, "listing occurrences")
	}

	out := make([]*fijopb.Occurrence, 0, len(occurrences))
	for _, occurrence := range occurrences {
		out = append(out, occurrenceToProto(occurrence))
	}

	return &fijopb.ListOccurrencesResponse{Occurrences: out}, nil
}

// CancelOccurrence implements fijopb.ScheduleServiceServer.
func (s *Server) CancelOccurrence(ctx context.Context, request *fijopb.CancelOccurrenceRequest) (*fijopb.OccurrenceResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	caller, scope, err := s.caller(ctx, op, fijopb.ScheduleService_CancelOccurrence_FullMethodName)
	if err != nil {
		return nil, err
	}

	by := cancellerFromProto(request.GetCancelledBy())
	if !by.Valid() {
		return nil, s.fail(platformerrors.Wrap(schedule.ErrInvalidOccurrence, "a cancellation says whose decision it was"), op, "cancelling an occurrence")
	}

	cancelled, err := s.commands.Cancel(ctx, scope, caller.UserID(), request.GetId(), by, request.GetReason())
	if err != nil {
		return nil, s.fail(err, op, "cancelling an occurrence")
	}

	return &fijopb.OccurrenceResponse{Occurrence: occurrenceToProto(cancelled)}, nil
}

// CompleteOccurrence implements fijopb.ScheduleServiceServer.
func (s *Server) CompleteOccurrence(ctx context.Context, request *fijopb.CompleteOccurrenceRequest) (*fijopb.OccurrenceResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	caller, scope, err := s.caller(ctx, op, fijopb.ScheduleService_CompleteOccurrence_FullMethodName)
	if err != nil {
		return nil, err
	}

	completed, err := s.commands.Complete(ctx, scope, caller.UserID(), request.GetId(), request.GetReason())
	if err != nil {
		return nil, s.fail(err, op, "completing an occurrence")
	}

	return &fijopb.OccurrenceResponse{Occurrence: occurrenceToProto(completed)}, nil
}

// NoShowOccurrence implements fijopb.ScheduleServiceServer.
func (s *Server) NoShowOccurrence(ctx context.Context, request *fijopb.NoShowOccurrenceRequest) (*fijopb.OccurrenceResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	caller, scope, err := s.caller(ctx, op, fijopb.ScheduleService_NoShowOccurrence_FullMethodName)
	if err != nil {
		return nil, err
	}

	marked, err := s.commands.NoShow(ctx, scope, caller.UserID(), request.GetId(), request.GetReason())
	if err != nil {
		return nil, s.fail(err, op, "marking an occurrence a no-show")
	}

	return &fijopb.OccurrenceResponse{Occurrence: occurrenceToProto(marked)}, nil
}

// RevertOccurrence implements fijopb.ScheduleServiceServer.
func (s *Server) RevertOccurrence(ctx context.Context, request *fijopb.RevertOccurrenceRequest) (*fijopb.OccurrenceResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	caller, scope, err := s.caller(ctx, op, fijopb.ScheduleService_RevertOccurrence_FullMethodName)
	if err != nil {
		return nil, err
	}

	reverted, err := s.commands.Revert(ctx, scope, caller.UserID(), request.GetId(), request.GetReason())
	if err != nil {
		return nil, s.fail(err, op, "reverting an occurrence")
	}

	return &fijopb.OccurrenceResponse{Occurrence: occurrenceToProto(reverted)}, nil
}

// MoveOccurrence implements fijopb.ScheduleServiceServer.
func (s *Server) MoveOccurrence(ctx context.Context, request *fijopb.MoveOccurrenceRequest) (*fijopb.OccurrenceResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	caller, scope, err := s.caller(ctx, op, fijopb.ScheduleService_MoveOccurrence_FullMethodName)
	if err != nil {
		return nil, err
	}

	if request.GetStartsAt() == nil {
		return nil, s.fail(platformerrors.Wrap(schedule.ErrInvalidOccurrence, "a move names a new time"), op, "moving an occurrence")
	}

	moved, err := s.commands.Move(ctx, scope, caller.UserID(), request.GetId(), request.GetStartsAt().AsTime(), request.GetReason())
	if err != nil {
		return nil, s.fail(err, op, "moving an occurrence")
	}

	return &fijopb.OccurrenceResponse{Occurrence: occurrenceToProto(moved)}, nil
}

// ScheduleMakeUp implements fijopb.ScheduleServiceServer.
func (s *Server) ScheduleMakeUp(ctx context.Context, request *fijopb.ScheduleMakeUpRequest) (*fijopb.OccurrenceResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	caller, scope, err := s.caller(ctx, op, fijopb.ScheduleService_ScheduleMakeUp_FullMethodName)
	if err != nil {
		return nil, err
	}

	if request.GetStartsAt() == nil {
		return nil, s.fail(platformerrors.Wrap(schedule.ErrInvalidOccurrence, "a make-up names a time"), op, "scheduling a make-up")
	}

	makeUp, err := s.commands.ScheduleMakeUp(ctx, scope, caller.UserID(), request.GetCancelledOccurrenceId(), request.GetStartsAt().AsTime(), request.GetReason())
	if err != nil {
		return nil, s.fail(err, op, "scheduling a make-up")
	}

	return &fijopb.OccurrenceResponse{Occurrence: occurrenceToProto(makeUp)}, nil
}

// ReassignOccurrence implements fijopb.ScheduleServiceServer.
func (s *Server) ReassignOccurrence(ctx context.Context, request *fijopb.ReassignOccurrenceRequest) (*fijopb.OccurrenceResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	caller, scope, err := s.caller(ctx, op, fijopb.ScheduleService_ReassignOccurrence_FullMethodName)
	if err != nil {
		return nil, err
	}

	reassigned, err := s.commands.Reassign(ctx, scope, caller.UserID(), request.GetId(), request.GetStaffUserIds())
	if err != nil {
		return nil, s.fail(err, op, "reassigning an occurrence")
	}

	return &fijopb.OccurrenceResponse{Occurrence: occurrenceToProto(reassigned)}, nil
}

// ListClosures implements fijopb.ScheduleServiceServer.
func (s *Server) ListClosures(ctx context.Context, _ *fijopb.ListClosuresRequest) (*fijopb.ListClosuresResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	_, scope, err := s.caller(ctx, op, fijopb.ScheduleService_ListClosures_FullMethodName)
	if err != nil {
		return nil, err
	}

	closures, err := s.store.ListClosures(ctx, s.client.Reader(), scope)
	if err != nil {
		return nil, s.fail(err, op, "listing closures")
	}

	out := make([]*fijopb.Closure, 0, len(closures))
	for _, closure := range closures {
		out = append(out, closureToProto(closure))
	}

	return &fijopb.ListClosuresResponse{Closures: out}, nil
}

// CloseStudio implements fijopb.ScheduleServiceServer.
func (s *Server) CloseStudio(ctx context.Context, request *fijopb.CloseStudioRequest) (*fijopb.CloseStudioResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	caller, scope, err := s.caller(ctx, op, fijopb.ScheduleService_CloseStudio_FullMethodName)
	if err != nil {
		return nil, err
	}

	startsOn, err := parseDate(request.GetStartsOn())
	if err != nil {
		return nil, s.fail(err, op, "closing the studio")
	}

	endsOn, err := parseDate(request.GetEndsOn())
	if err != nil {
		return nil, s.fail(err, op, "closing the studio")
	}

	closure, err := s.commands.CloseStudio(ctx, scope, caller.UserID(), startsOn, endsOn, request.GetReason())
	if err != nil {
		return nil, s.fail(err, op, "closing the studio")
	}

	return &fijopb.CloseStudioResponse{Closure: closureToProto(closure)}, nil
}

// ReopenStudio implements fijopb.ScheduleServiceServer.
func (s *Server) ReopenStudio(ctx context.Context, request *fijopb.ReopenStudioRequest) (*fijopb.ReopenStudioResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	_, scope, err := s.caller(ctx, op, fijopb.ScheduleService_ReopenStudio_FullMethodName)
	if err != nil {
		return nil, err
	}

	if err = s.client.WithTransaction(ctx, func(tx database.Tx) error {
		if archiveErr := s.store.ArchiveClosure(ctx, tx, scope, request.GetId()); archiveErr != nil {
			return archiveErr
		}

		return s.syncStudio(ctx, tx, scope)
	}); err != nil {
		return nil, s.fail(err, op, "reopening the studio")
	}

	return &fijopb.ReopenStudioResponse{}, nil
}

// ListHistory implements fijopb.ScheduleServiceServer.
//
// This is Fijo's own read of the audit log rather than the platform's mounted
// one. The platform's derives its scope from the principal's directory scope,
// which is global here by design, so it cannot see a studio's entries; this
// asks in the studio's scope, which is the scope the commands recorded under.
// See platform-go#875.
func (s *Server) ListHistory(ctx context.Context, request *fijopb.ListHistoryRequest) (*fijopb.ListHistoryResponse, error) {
	ctx, op, end := s.begin(ctx)
	defer end()

	_, scope, err := s.caller(ctx, op, fijopb.ScheduleService_ListHistory_FullMethodName)
	if err != nil {
		return nil, err
	}

	if s.history == nil {
		return nil, s.unavailable(op, ErrNoHistory, "reading the studio's history")
	}

	filter := filtering.DefaultQueryFilter()
	if limit := request.GetLimit(); limit > 0 {
		filter.MaxResponseSize = new(uint16(min(limit, maxHistoryLimit)))
	}

	entries, err := s.history.List(ctx, s.client.Reader(), &audit.Query{
		Scope:        &scope,
		ResourceType: request.GetResourceType(),
		ResourceID:   request.GetResourceId(),
	}, filter)
	if err != nil {
		return nil, s.fail(err, op, "reading the studio's history")
	}

	out := make([]*fijopb.HistoryEntry, 0, len(entries.Data))
	for _, entry := range entries.Data {
		out = append(out, entryToProto(entry))
	}

	return &fijopb.ListHistoryResponse{Entries: out}, nil
}

func settingsToProto(settings *schedule.Settings) *fijopb.ScheduleSettings {
	if settings == nil {
		return nil
	}

	return &fijopb.ScheduleSettings{
		NoticeWindowMinutes: int32(settings.NoticeWindow / time.Minute),
		HorizonWeeks:        int32(settings.HorizonWeeks),
	}
}

func offeringToProto(offering *schedule.Offering) *fijopb.Offering {
	if offering == nil {
		return nil
	}

	out := &fijopb.Offering{
		Id:              offering.ID,
		NameEn:          offering.NameEN,
		NameEs:          offering.NameES,
		Kind:            offeringKindToProto(offering.Kind),
		DurationMinutes: int32(offering.Duration / time.Minute),
		ListPrice:       offering.ListPrice,
		LessonCount:     int32(offering.LessonCount),
		Shareable:       offering.Shareable,
		Archived:        offering.ArchivedAt != nil,
	}

	if offering.ExpiresAfter > 0 {
		out.ExpiresAfterDays = int32(offering.ExpiresAfter / (24 * time.Hour))
	}

	return out
}

func offeringFromProto(in *fijopb.Offering) *schedule.Offering {
	if in == nil {
		return nil
	}

	return &schedule.Offering{
		ID:           in.GetId(),
		NameEN:       in.GetNameEn(),
		NameES:       in.GetNameEs(),
		Kind:         offeringKindFromProto(in.GetKind()),
		Duration:     time.Duration(in.GetDurationMinutes()) * time.Minute,
		ListPrice:    in.GetListPrice(),
		LessonCount:  int(in.GetLessonCount()),
		ExpiresAfter: time.Duration(in.GetExpiresAfterDays()) * 24 * time.Hour,
		Shareable:    in.GetShareable(),
	}
}

func seriesToProto(series *schedule.Series) *fijopb.Series {
	if series == nil {
		return nil
	}

	out := &fijopb.Series{
		Id:            series.ID,
		OfferingId:    series.OfferingID,
		Weekday:       int32(series.Weekday),
		StartMinute:   int32(series.StartTime.Minutes()),
		IntervalWeeks: int32(series.IntervalWeeks),
		StartsOn:      series.StartsOn.String(),
		StaffUserIds:  series.Staff,
		Archived:      series.ArchivedAt != nil,
	}

	if !series.EndsOn.IsZero() {
		out.EndsOn = series.EndsOn.String()
	}

	if series.PriceOverride != nil {
		out.PriceOverride = series.PriceOverride
	}

	return out
}

func seriesFromProto(in *fijopb.Series) (*schedule.Series, error) {
	if in == nil {
		return nil, schedule.ErrNilInput
	}

	startsOn, err := parseDate(in.GetStartsOn())
	if err != nil {
		return nil, err
	}

	out := &schedule.Series{
		ID:            in.GetId(),
		OfferingID:    in.GetOfferingId(),
		Weekday:       time.Weekday(in.GetWeekday()),
		StartTime:     schedule.ClockTimeOfMinutes(int(in.GetStartMinute())),
		IntervalWeeks: int(in.GetIntervalWeeks()),
		StartsOn:      startsOn,
		Staff:         in.GetStaffUserIds(),
	}

	if raw := in.GetEndsOn(); raw != "" {
		endsOn, endErr := parseDate(raw)
		if endErr != nil {
			return nil, endErr
		}

		out.EndsOn = endsOn
	}

	if in.PriceOverride != nil {
		override := in.GetPriceOverride()
		out.PriceOverride = &override
	}

	return out, nil
}

func occurrenceToProto(occurrence *schedule.Occurrence) *fijopb.Occurrence {
	if occurrence == nil {
		return nil
	}

	return &fijopb.Occurrence{
		Id:                    occurrence.ID,
		SeriesId:              occurrence.SeriesID,
		OfferingId:            occurrence.OfferingID,
		StartsAt:              timestamppb.New(occurrence.StartsAt),
		EndsAt:                timestamppb.New(occurrence.EndsAt),
		State:                 occurrenceStateToProto(occurrence.State),
		CancelledBy:           cancellerToProto(occurrence.CancelledBy),
		CancelledInsideNotice: occurrence.CancelledInsideNotice,
		Reason:                occurrence.Reason,
		MovedToOccurrenceId:   occurrence.MovedTo,
		ReplacesOccurrenceId:  occurrence.Replaces,
		StaffUserIds:          occurrence.Staff,
	}
}

func closureToProto(closure *schedule.Closure) *fijopb.Closure {
	if closure == nil {
		return nil
	}

	return &fijopb.Closure{
		Id:       closure.ID,
		StartsOn: closure.StartsOn.String(),
		EndsOn:   closure.EndsOn.String(),
		Reason:   closure.Reason,
	}
}

func entryToProto(entry *audit.Entry) *fijopb.HistoryEntry {
	if entry == nil {
		return nil
	}

	return &fijopb.HistoryEntry{
		Id:           entry.ID,
		OccurredAt:   timestamppb.New(entry.RecordedAt),
		ActorUserId:  entry.Actor.ID,
		ResourceType: entry.ResourceType,
		ResourceId:   entry.ResourceID,
		EventType:    string(entry.EventType),
		Metadata:     entry.Metadata,
	}
}

func offeringKindToProto(kind schedule.OfferingKind) fijopb.OfferingKind {
	switch kind {
	case schedule.KindSingle:
		return fijopb.OfferingKind_OFFERING_KIND_SINGLE
	case schedule.KindPackage:
		return fijopb.OfferingKind_OFFERING_KIND_PACKAGE
	case schedule.KindRecurring:
		return fijopb.OfferingKind_OFFERING_KIND_RECURRING
	default:
		return fijopb.OfferingKind_OFFERING_KIND_UNSPECIFIED
	}
}

func offeringKindFromProto(kind fijopb.OfferingKind) schedule.OfferingKind {
	switch kind {
	case fijopb.OfferingKind_OFFERING_KIND_SINGLE:
		return schedule.KindSingle
	case fijopb.OfferingKind_OFFERING_KIND_PACKAGE:
		return schedule.KindPackage
	case fijopb.OfferingKind_OFFERING_KIND_RECURRING:
		return schedule.KindRecurring
	case fijopb.OfferingKind_OFFERING_KIND_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}

func occurrenceStateToProto(state schedule.OccurrenceState) fijopb.OccurrenceState {
	switch state {
	case schedule.StateScheduled:
		return fijopb.OccurrenceState_OCCURRENCE_STATE_SCHEDULED
	case schedule.StateCompleted:
		return fijopb.OccurrenceState_OCCURRENCE_STATE_COMPLETED
	case schedule.StateCancelled:
		return fijopb.OccurrenceState_OCCURRENCE_STATE_CANCELLED
	case schedule.StateNoShow:
		return fijopb.OccurrenceState_OCCURRENCE_STATE_NO_SHOW
	case schedule.StateMoved:
		return fijopb.OccurrenceState_OCCURRENCE_STATE_MOVED
	default:
		return fijopb.OccurrenceState_OCCURRENCE_STATE_UNSPECIFIED
	}
}

func cancellerToProto(by schedule.Canceller) fijopb.Canceller {
	switch by {
	case schedule.CancelledByHousehold:
		return fijopb.Canceller_CANCELLER_HOUSEHOLD
	case schedule.CancelledByStudio:
		return fijopb.Canceller_CANCELLER_STUDIO
	case schedule.CancelledByClosure:
		return fijopb.Canceller_CANCELLER_CLOSURE
	default:
		return fijopb.Canceller_CANCELLER_UNSPECIFIED
	}
}

func cancellerFromProto(by fijopb.Canceller) schedule.Canceller {
	switch by {
	case fijopb.Canceller_CANCELLER_HOUSEHOLD:
		return schedule.CancelledByHousehold
	case fijopb.Canceller_CANCELLER_STUDIO:
		return schedule.CancelledByStudio
	case fijopb.Canceller_CANCELLER_CLOSURE:
		return schedule.CancelledByClosure
	case fijopb.Canceller_CANCELLER_UNSPECIFIED:
		return ""
	default:
		return ""
	}
}

// parseDate reads the ISO civil date the wire carries.
func parseDate(raw string) (schedule.Date, error) {
	if raw == "" {
		return schedule.Date{}, platformerrors.Wrap(platformerrors.ErrUnrecognizedInputValue, "empty date")
	}

	parsed, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		return schedule.Date{}, platformerrors.Wrapf(platformerrors.ErrUnrecognizedInputValue, "%q is not an ISO date", raw)
	}

	return schedule.DateOf(parsed), nil
}
