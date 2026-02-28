package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	authpb "auth_service/proto"
	nexusai "nexus/proto/nexusai/v1"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type analyzeRequest struct {
	UserTZ      string      `json:"user_tz"`
	WeekStarts  string      `json:"week_starts"`
	Constraints constraints `json:"constraints"`
	Period      string      `json:"period"`
}

type todayTrackRequest struct {
	UserTZ string `json:"user_tz"`
}

type trackRequest struct {
	UserTZ string       `json:"user_tz"`
	Points []trackPoint `json:"points"`
}

type trackPoint struct {
	TS            time.Time `json:"ts"`
	SleepHours    float64   `json:"sleep_hours"`
	SleepStart    string    `json:"sleep_start"`
	SleepEnd      string    `json:"sleep_end"`
	Mood          float64   `json:"mood"`
	Activity      float64   `json:"activity"`
	Productive    float64   `json:"productive"`
	Stress        float64   `json:"stress"`
	Energy        float64   `json:"energy"`
	Concentration float64   `json:"concentration"`
	SleepQuality  float64   `json:"sleep_quality"`
	Caffeine      bool      `json:"caffeine"`
	Alcohol       bool      `json:"alcohol"`
	Workout       bool      `json:"workout"`
	LLMText       string    `json:"llm_text"`
	AnalysisStatus string   `json:"analysis_status"`
}

type userProfile struct {
	UserID  int32  `json:"user_id"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Emoji   string `json:"emoji"`
	BgIndex int32  `json:"bg_index"`
	IsFriend bool  `json:"is_friend"`
}

type friendRequest struct {
	ID        int64       `json:"id"`
	Status    string      `json:"status"`
	CreatedAt time.Time   `json:"created_at"`
	From      userProfile `json:"from"`
	To        userProfile `json:"to"`
}

type constraints struct {
	WorkStartHour int `json:"work_start_hour"`
	WorkEndHour   int `json:"work_end_hour"`
}

type analyzeResponse struct {
	EnergyByWeekday   map[string]float64 `json:"energy_by_weekday"`
	ProductivityModel productivityModel  `json:"productivity_model"`
	BurnoutRisk       burnoutRisk        `json:"burnout_risk"`
	OptimalSchedule   optimalSchedule    `json:"optimal_schedule"`
	LLMInsight        string             `json:"llm_insight"`
	Debug             map[string]any     `json:"debug,omitempty"`
}

type productivityModel struct {
	Weights map[string]float64 `json:"weights"`
	Score   float64            `json:"score"`
}

type burnoutRisk struct {
	Score                 float64  `json:"score"`
	Level                 string   `json:"level"`
	Reasons               []string `json:"reasons"`
	PredictionHorizonDays int      `json:"prediction_horizon_days"`
}

type optimalSchedule struct {
	SuggestedSleepWindow string   `json:"suggested_sleep_window"`
	BestFocusHours       []string `json:"best_focus_hours"`
	BestLightTasksHours  []string `json:"best_light_tasks_hours"`
	RecoveryTips         []string `json:"recovery_tips"`
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8088"
	}

	target := os.Getenv("NEXUS_AI_ADDR")
	if target == "" {
		target = "nexus_ai:9091"
	}

	authTarget := os.Getenv("AUTH_SERVICE_ADDR")
	if authTarget == "" {
		authTarget = "auth_service:9090"
	}

	aiConn, err := grpc.Dial(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("grpc dial: %v", err)
	}
	defer aiConn.Close()

	authConn, err := grpc.Dial(authTarget, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("auth grpc dial: %v", err)
	}
	defer authConn.Close()

	aiClient := nexusai.NewAnalyzerServiceClient(aiConn)

	app := fiber.New()
	app.Use(cors.New(cors.Config{
		AllowOrigins: []string{"*"},
		AllowHeaders: []string{"Authorization", "Content-Type"},
		AllowMethods: []string{"GET", "POST", "OPTIONS"},
	}))

	app.Get("/health", func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})
	app.Post("/ai/analyze", func(c fiber.Ctx) error {
		return handleAnalyze(c, aiClient)
	})
	app.Post("/ai/track", func(c fiber.Ctx) error {
		return handleTrack(c, aiClient)
	})
	app.Post("/ai/today", func(c fiber.Ctx) error {
		return handleToday(c, aiClient)
	})
	app.Get("/ai/last-analyze", func(c fiber.Ctx) error {
		return handleLastAnalyze(c, aiClient)
	})
	app.Get("/ai/profile", func(c fiber.Ctx) error {
		return handleGetProfile(c, aiClient)
	})
	app.Post("/ai/profile", func(c fiber.Ctx) error {
		return handleUpdateProfile(c, aiClient)
	})
	app.Get("/ai/users/:id", func(c fiber.Ctx) error {
		return handleGetUserProfile(c, aiClient)
	})
	app.Get("/ai/users/:id/last-analyses", func(c fiber.Ctx) error {
		return handleGetUserLastAnalyses(c, aiClient)
	})
	app.Get("/ai/friends", func(c fiber.Ctx) error {
		return handleListFriends(c, aiClient)
	})
	app.Get("/ai/friends/requests", func(c fiber.Ctx) error {
		return handleListFriendRequests(c, aiClient)
	})
	app.Get("/ai/friends/search", func(c fiber.Ctx) error {
		return handleSearchUsers(c, aiClient)
	})
	app.Post("/ai/friends/request", func(c fiber.Ctx) error {
		return handleSendFriendRequest(c, aiClient)
	})
	app.Post("/ai/friends/respond", func(c fiber.Ctx) error {
		return handleRespondFriendRequest(c, aiClient)
	})

	gwMux := runtime.NewServeMux(runtime.WithIncomingHeaderMatcher(incomingHeaderMatcher))
	if err := authpb.RegisterAuthServiceHandler(context.Background(), gwMux, authConn); err != nil {
		log.Fatalf("register auth gateway: %v", err)
	}

	app.All("/auth/*", adaptor.HTTPHandler(gwMux))
	app.All("/auth", adaptor.HTTPHandler(gwMux))

	log.Printf("gateway listening on :%s", port)
	if err := app.Listen(":" + port); err != nil {
		log.Fatal(err)
	}
}

func handleAnalyze(c fiber.Ctx, client nexusai.AnalyzerServiceClient) error {
	var in analyzeRequest
	if err := json.Unmarshal(c.Body(), &in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "bad json: "+err.Error())
	}

	grpcReq, err := mapRequest(in)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	timeout := 60 * time.Second
	if v := os.Getenv("ANALYZE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			timeout = d
		}
	}
	ctx, cancel := context.WithTimeout(c.Context(), timeout)
	defer cancel()
	ctx = withAuthMetadata(ctx, c.Get("Authorization"))

	resp, err := client.Analyze(ctx, grpcReq)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "analyze error: "+err.Error())
	}

	out, err := mapResponse(resp)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "response error: "+err.Error())
	}

	return c.JSON(out)
}

func handleTrack(c fiber.Ctx, client nexusai.AnalyzerServiceClient) error {
	var in trackRequest
	if err := json.Unmarshal(c.Body(), &in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "bad json: "+err.Error())
	}
	if len(in.Points) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "points are required")
	}

	grpcReq, err := mapTrackRequest(in)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	timeout := 60 * time.Second
	if v := os.Getenv("TRACK_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			timeout = d
		}
	}
	ctx, cancel := context.WithTimeout(c.Context(), timeout)
	defer cancel()
	ctx = withAuthMetadata(ctx, c.Get("Authorization"))

	resp, err := client.Track(ctx, grpcReq)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "track error: "+err.Error())
	}

	return c.JSON(fiber.Map{"stored": resp.GetStored()})
}

func handleToday(c fiber.Ctx, client nexusai.AnalyzerServiceClient) error {
	var in todayTrackRequest
	_ = json.Unmarshal(c.Body(), &in)
	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()
	ctx = withAuthMetadata(ctx, c.Get("Authorization"))

	resp, err := client.GetTodayTrack(ctx, &nexusai.TodayTrackRequest{UserTz: in.UserTZ})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "today error: "+err.Error())
	}
	if !resp.GetExists() || resp.GetPoint() == nil {
		return c.JSON(fiber.Map{"exists": false})
	}
	p := resp.GetPoint()
	out := trackPoint{
		TS:            p.GetTs().AsTime(),
		SleepHours:    p.GetSleepHours(),
		SleepStart:    p.GetSleepStart(),
		SleepEnd:      p.GetSleepEnd(),
		Mood:          p.GetMood(),
		Activity:      p.GetActivity(),
		Productive:    p.GetProductive(),
		Stress:        p.GetStress(),
		Energy:        p.GetEnergy(),
		Concentration: p.GetConcentration(),
		SleepQuality:  p.GetSleepQuality(),
		Caffeine:      p.GetCaffeine(),
		Alcohol:       p.GetAlcohol(),
		Workout:       p.GetWorkout(),
		LLMText:       p.GetLlmText(),
		AnalysisStatus: p.GetAnalysisStatus(),
	}
	return c.JSON(fiber.Map{"exists": true, "point": out})
}

func handleGetProfile(c fiber.Ctx, client nexusai.AnalyzerServiceClient) error {
	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()
	ctx = withAuthMetadata(ctx, c.Get("Authorization"))

	resp, err := client.GetMyProfile(ctx, &nexusai.GetMyProfileRequest{})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "profile error: "+err.Error())
	}
	if resp.GetProfile() == nil {
		return c.JSON(fiber.Map{})
	}
	return c.JSON(mapUserProfile(resp.GetProfile()))
}

type updateProfileRequest struct {
	Emoji   string `json:"emoji"`
	BgIndex int32  `json:"bg_index"`
}

func handleUpdateProfile(c fiber.Ctx, client nexusai.AnalyzerServiceClient) error {
	var in updateProfileRequest
	if err := json.Unmarshal(c.Body(), &in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "bad json: "+err.Error())
	}
	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()
	ctx = withAuthMetadata(ctx, c.Get("Authorization"))

	resp, err := client.UpdateMyProfile(ctx, &nexusai.UpdateProfileRequest{
		Emoji:   in.Emoji,
		BgIndex: in.BgIndex,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "profile update error: "+err.Error())
	}
	if resp.GetProfile() == nil {
		return c.JSON(fiber.Map{})
	}
	return c.JSON(mapUserProfile(resp.GetProfile()))
}

func handleGetUserProfile(c fiber.Ctx, client nexusai.AnalyzerServiceClient) error {
	userIDStr := c.Params("id")
	userID, err := strconv.Atoi(userIDStr)
	if err != nil || userID <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid user id")
	}
	ctx, cancel := context.WithTimeout(c.Context(), 8*time.Second)
	defer cancel()
	ctx = withAuthMetadata(ctx, c.Get("Authorization"))
	resp, err := client.GetUserProfile(ctx, &nexusai.GetUserProfileRequest{UserId: int32(userID)})
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "permission") || strings.Contains(msg, "denied") {
			return fiber.NewError(fiber.StatusForbidden, "profile is private")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "profile error: "+msg)
	}
	if resp.GetProfile() == nil {
		return fiber.NewError(fiber.StatusNotFound, "profile not found")
	}
	return c.JSON(mapUserProfile(resp.GetProfile()))
}

func handleListFriends(c fiber.Ctx, client nexusai.AnalyzerServiceClient) error {
	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()
	ctx = withAuthMetadata(ctx, c.Get("Authorization"))

	resp, err := client.ListFriends(ctx, &nexusai.ListFriendsRequest{})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "friends error: "+err.Error())
	}
	out := make([]userProfile, 0, len(resp.GetFriends()))
	for _, u := range resp.GetFriends() {
		out = append(out, mapUserProfile(u))
	}
	return c.JSON(fiber.Map{"friends": out})
}

func handleListFriendRequests(c fiber.Ctx, client nexusai.AnalyzerServiceClient) error {
	status := c.Query("status", "pending")
	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()
	ctx = withAuthMetadata(ctx, c.Get("Authorization"))

	resp, err := client.ListFriendRequests(ctx, &nexusai.ListFriendRequestsRequest{Status: status})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "friend requests error: "+err.Error())
	}
	out := make([]friendRequest, 0, len(resp.GetRequests()))
	for _, r := range resp.GetRequests() {
		out = append(out, mapFriendRequest(r))
	}
	return c.JSON(fiber.Map{"requests": out})
}

func handleSearchUsers(c fiber.Ctx, client nexusai.AnalyzerServiceClient) error {
	query := c.Query("q", "")
	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()
	ctx = withAuthMetadata(ctx, c.Get("Authorization"))

	resp, err := client.SearchUsers(ctx, &nexusai.SearchUsersRequest{Query: query})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "search error: "+err.Error())
	}
	out := make([]userProfile, 0, len(resp.GetUsers()))
	for _, u := range resp.GetUsers() {
		out = append(out, mapUserProfile(u))
	}
	return c.JSON(fiber.Map{"users": out})
}

type sendFriendRequest struct {
	ToUserID int32 `json:"to_user_id"`
}

func handleSendFriendRequest(c fiber.Ctx, client nexusai.AnalyzerServiceClient) error {
	var in sendFriendRequest
	if err := json.Unmarshal(c.Body(), &in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "bad json: "+err.Error())
	}
	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()
	ctx = withAuthMetadata(ctx, c.Get("Authorization"))

	resp, err := client.SendFriendRequest(ctx, &nexusai.SendFriendRequestRequest{ToUserId: in.ToUserID})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "friend request error: "+err.Error())
	}
	if resp.GetRequest() == nil {
		return c.JSON(fiber.Map{})
	}
	return c.JSON(mapFriendRequest(resp.GetRequest()))
}

type respondFriendRequest struct {
	RequestID int64  `json:"request_id"`
	Action    string `json:"action"`
}

func handleRespondFriendRequest(c fiber.Ctx, client nexusai.AnalyzerServiceClient) error {
	var in respondFriendRequest
	if err := json.Unmarshal(c.Body(), &in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "bad json: "+err.Error())
	}
	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()
	ctx = withAuthMetadata(ctx, c.Get("Authorization"))

	_, err := client.RespondFriendRequest(ctx, &nexusai.RespondFriendRequestRequest{
		RequestId: in.RequestID,
		Action:    in.Action,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "friend respond error: "+err.Error())
	}
	return c.JSON(fiber.Map{"ok": true})
}

func handleLastAnalyze(c fiber.Ctx, client nexusai.AnalyzerServiceClient) error {
	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()
	ctx = withAuthMetadata(ctx, c.Get("Authorization"))

	resp, err := client.GetLastAnalyses(ctx, &nexusai.LastAnalysesRequest{})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "last analyze error: "+err.Error())
	}
	out := make([]fiber.Map, 0, len(resp.GetEntries()))
	for _, e := range resp.GetEntries() {
		if e == nil || e.Response == nil {
			continue
		}
		mapped, err := mapResponse(e.Response)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "response error: "+err.Error())
		}
		out = append(out, fiber.Map{
			"period":     e.GetPeriod(),
			"updated_at": e.GetUpdatedAt().AsTime(),
			"response":   mapped,
		})
	}
	return c.JSON(fiber.Map{"entries": out})
}

func handleGetUserLastAnalyses(c fiber.Ctx, client nexusai.AnalyzerServiceClient) error {
	userIDStr := c.Params("id")
	userID, err := strconv.Atoi(userIDStr)
	if err != nil || userID <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid user id")
	}
	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()
	ctx = withAuthMetadata(ctx, c.Get("Authorization"))

	resp, err := client.GetUserLastAnalyses(ctx, &nexusai.GetUserLastAnalysesRequest{UserId: int32(userID)})
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "permission") || strings.Contains(msg, "denied") {
			return fiber.NewError(fiber.StatusForbidden, "profile is private")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "last analyze error: "+msg)
	}
	out := make([]fiber.Map, 0, len(resp.GetEntries()))
	for _, e := range resp.GetEntries() {
		if e == nil || e.Response == nil {
			continue
		}
		mapped, err := mapResponse(e.Response)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "response error: "+err.Error())
		}
		out = append(out, fiber.Map{
			"period":     e.GetPeriod(),
			"updated_at": e.GetUpdatedAt().AsTime(),
			"response":   mapped,
		})
	}
	return c.JSON(fiber.Map{"entries": out})
}

func mapRequest(in analyzeRequest) (*nexusai.AnalyzeRequest, error) {
	req := &nexusai.AnalyzeRequest{
		UserTz:     in.UserTZ,
		WeekStarts: in.WeekStarts,
		Constraints: &nexusai.Constraints{
			WorkStartHour: int32(in.Constraints.WorkStartHour),
			WorkEndHour:   int32(in.Constraints.WorkEndHour),
		},
		Period: mapPeriod(in.Period),
	}
	return req, nil
}

func mapTrackRequest(in trackRequest) (*nexusai.TrackRequest, error) {
	points := make([]*nexusai.TrackPoint, 0, len(in.Points))
	for _, p := range in.Points {
		if p.TS.IsZero() {
			return nil, errors.New("point ts is required")
		}
		points = append(points, &nexusai.TrackPoint{
			Ts:            timestamppb.New(p.TS),
			SleepHours:    p.SleepHours,
			SleepStart:    p.SleepStart,
			SleepEnd:      p.SleepEnd,
			Mood:          p.Mood,
			Activity:      p.Activity,
			Productive:    p.Productive,
			Stress:        p.Stress,
			Energy:        p.Energy,
			Concentration: p.Concentration,
			SleepQuality:  p.SleepQuality,
			Caffeine:      p.Caffeine,
			Alcohol:       p.Alcohol,
			Workout:       p.Workout,
			LlmText:       p.LLMText,
		})
	}

	return &nexusai.TrackRequest{
		UserTz: in.UserTZ,
		Points: points,
	}, nil
}

func mapResponse(in *nexusai.AnalyzeResponse) (*analyzeResponse, error) {
	if in == nil {
		return nil, errors.New("empty response")
	}

	energyByWeekday := make(map[string]float64, len(in.EnergyByWeekday))
	for k, v := range in.EnergyByWeekday {
		energyByWeekday[k] = v
	}

	weights := make(map[string]float64, len(in.ProductivityModel.GetWeights()))
	for k, v := range in.ProductivityModel.GetWeights() {
		weights[k] = v
	}

	out := &analyzeResponse{
		EnergyByWeekday: energyByWeekday,
		ProductivityModel: productivityModel{
			Weights: weights,
			Score:   in.ProductivityModel.GetScore(),
		},
		BurnoutRisk: burnoutRisk{
			Score:                 in.BurnoutRisk.GetScore(),
			Level:                 in.BurnoutRisk.GetLevel(),
			Reasons:               append([]string(nil), in.BurnoutRisk.GetReasons()...),
			PredictionHorizonDays: int(in.BurnoutRisk.GetPredictionHorizonDays()),
		},
		OptimalSchedule: optimalSchedule{
			SuggestedSleepWindow: in.OptimalSchedule.GetSuggestedSleepWindow(),
			BestFocusHours:       append([]string(nil), in.OptimalSchedule.GetBestFocusHours()...),
			BestLightTasksHours:  append([]string(nil), in.OptimalSchedule.GetBestLightTasksHours()...),
			RecoveryTips:         append([]string(nil), in.OptimalSchedule.GetRecoveryTips()...),
		},
		LLMInsight: in.GetLlmInsight(),
	}

	if in.Debug != nil {
		m, err := structpbToMap(in.Debug)
		if err != nil {
			return nil, err
		}
		out.Debug = m
	}

	return out, nil
}

func structpbToMap(s *structpb.Struct) (map[string]any, error) {
	if s == nil {
		return nil, nil
	}
	b, err := s.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func intKey(v int32) string {
	return strconv.FormatInt(int64(v), 10)
}

func withAuthMetadata(ctx context.Context, authHeader string) context.Context {
	auth := strings.TrimSpace(authHeader)
	if auth == "" {
		return ctx
	}
	md := metadata.Pairs("authorization", auth)
	return metadata.NewOutgoingContext(ctx, md)
}

func incomingHeaderMatcher(key string) (string, bool) {
	if strings.EqualFold(key, "authorization") {
		return "authorization", true
	}
	if strings.EqualFold(key, "x-request-id") {
		return "x-request-id", true
	}
	return runtime.DefaultHeaderMatcher(key)
}

func mapUserProfile(u *nexusai.UserProfile) userProfile {
	if u == nil {
		return userProfile{}
	}
	return userProfile{
		UserID:  u.GetUserId(),
		Name:    u.GetName(),
		Email:   u.GetEmail(),
		Emoji:   u.GetEmoji(),
		BgIndex: u.GetBgIndex(),
		IsFriend: u.GetIsFriend(),
	}
}

func mapFriendRequest(r *nexusai.FriendRequest) friendRequest {
	if r == nil {
		return friendRequest{}
	}
	return friendRequest{
		ID:        r.GetId(),
		Status:    r.GetStatus(),
		CreatedAt: r.GetCreatedAt().AsTime(),
		From:      mapUserProfile(r.GetFrom()),
		To:        mapUserProfile(r.GetTo()),
	}
}

func mapPeriod(v string) nexusai.Period {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "day":
		return nexusai.Period_PERIOD_DAY
	case "week":
		return nexusai.Period_PERIOD_WEEK
	case "month":
		return nexusai.Period_PERIOD_MONTH
	case "all":
		return nexusai.Period_PERIOD_ALL
	default:
		return nexusai.Period_PERIOD_UNSPECIFIED
	}
}
