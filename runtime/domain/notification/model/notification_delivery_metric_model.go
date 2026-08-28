package notificationmodel

type NotificationDeliveryMetricBucket struct {
	Key        string `json:"key"`
	Total      int    `json:"total"`
	Queued     int    `json:"queued"`
	Sent       int    `json:"sent"`
	Delivered  int    `json:"delivered"`
	Read       int    `json:"read"`
	Failed     int    `json:"failed"`
	DeadLetter int    `json:"dead_letter"`
	Fallbacks  int    `json:"fallbacks"`
}

type NotificationDeliveryMetrics struct {
	Since       string                              `json:"since"`
	GeneratedAt string                              `json:"generated_at"`
	Summary     NotificationDeliveryMetricBucket    `json:"summary"`
	ByChannel   []NotificationDeliveryMetricBucket  `json:"by_channel"`
	ByTemplate  []NotificationDeliveryMetricBucket  `json:"by_template"`
	Failures    []NotificationDeliveryFailureMetric `json:"failures"`
}

type NotificationDeliveryFailureMetric struct {
	Error string `json:"error"`
	Count int    `json:"count"`
}
