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

type trackRequest struct {
	UserTZ string       `json:"user_tz"`
	Points []trackPoint `json:"points"`
}

type trackPoint struct {
	TS         time.Time `json:"ts"`
	SleepHours float64   `json:"sleep_hours"`
	Mood       float64   `json:"mood"`
	Activity   float64   `json:"activity"`
	Productive float64   `json:"productive"`
}

type constraints struct {
	WorkStartHour int `json:"work_start_hour"`
	WorkEndHour   int `json:"work_end_hour"`
}

type analyzeResponse struct {
	EnergyByHour      map[string]float64 `json:"energy_by_hour"`
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

	ctx, cancel := context.WithTimeout(c.Context(), 30*time.Second)
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

	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()
	ctx = withAuthMetadata(ctx, c.Get("Authorization"))

	resp, err := client.Track(ctx, grpcReq)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "track error: "+err.Error())
	}

	return c.JSON(fiber.Map{"stored": resp.GetStored()})
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
			Ts:         timestamppb.New(p.TS),
			SleepHours: p.SleepHours,
			Mood:       p.Mood,
			Activity:   p.Activity,
			Productive: p.Productive,
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

	energyByHour := make(map[string]float64, len(in.EnergyByHour))
	for k, v := range in.EnergyByHour {
		energyByHour[intKey(k)] = v
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
		EnergyByHour:    energyByHour,
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
