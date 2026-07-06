package protocol

// EventType is the discriminator carried in the WSS event envelope. Events are
// lightweight notifications only — they never carry clipboard bodies, file
// names, image dimensions, device private keys or tokens.
type EventType string

const (
	// EventDeliveryCreated tells a target device a new delivery is available.
	EventDeliveryCreated EventType = "clipboard.delivery.created"
	// EventDeliveryResolved tells the source device an item's deliveries are done.
	EventDeliveryResolved EventType = "clipboard.delivery.resolved"
	// EventConfigChanged tells a user's online devices to re-fetch effective config.
	EventConfigChanged EventType = "config.changed"
	// EventPairingRequested tells a user's Web sessions a device awaits confirmation.
	EventPairingRequested EventType = "pairing.requested"
	// EventDeviceRevoked tells a device it has been revoked; disconnect follows.
	EventDeviceRevoked EventType = "device.revoked"
	// EventServerNotice carries a stable announcement code to online devices.
	EventServerNotice EventType = "server.notice"
)

// Event is the WSS event envelope. Data is event-specific and intentionally
// minimal; receivers pull authoritative state over HTTPS after a notification.
type Event struct {
	Event      EventType `json:"event"`
	OccurredAt string    `json:"occurred_at"`
	Data       any       `json:"data,omitempty"`
}

// DeliveryCreatedData is the payload for EventDeliveryCreated.
type DeliveryCreatedData struct {
	DeliveryID string `json:"delivery_id"`
}

// DeliveryResolvedData is the payload for EventDeliveryResolved.
type DeliveryResolvedData struct {
	ItemID         string `json:"item_id"`
	AggregateState string `json:"aggregate_state"`
}

// PairingRequestedData is the payload for EventPairingRequested. It carries only
// the non-sensitive request metadata the Web console needs to render a prompt.
type PairingRequestedData struct {
	RequestID     string `json:"request_id"`
	DeviceName    string `json:"device_name"`
	Platform      string `json:"platform"`
	ClientVersion string `json:"client_version"`
}

// ServerNoticeData is the payload for EventServerNotice.
type ServerNoticeData struct {
	NoticeCode string `json:"notice_code"`
}
