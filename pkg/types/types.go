package types


type PubSubMessage struct {
	Attributes PubSubAttributes `json:"attributes"`
	Data       []byte           `json:"data"`
}

// PubSubAttributes are attributes from the Pub/Sub event.
type PubSubAttributes struct {
	SecretId  string
	EventType string
  DateFormat string
  Timestamp string
  VersionId string
  DeleteType string
}

type SecretRotationHandler interface {
  Name() string;
  Handle(msg PubSubMessage) error;
}
