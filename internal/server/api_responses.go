package server

import (
	"time"
)

// This file defines the wire format of the /api/v1 JSON API. api/openapi.yaml
// is the source of truth for the contract; these structs must stay in sync
// with it. Unsigned 64-bit counters are serialized as decimal strings because
// JSON numbers lose precision past 2^53.

// collectionResponse describes one collection pass across the configured
// ClickHouse nodes.
type collectionResponse struct {
	CollectedAt          time.Time         `json:"collectedAt"`
	Partial              bool              `json:"partial"`
	CollectionDurationMs int               `json:"collectionDurationMs"`
	NodesExpected        int               `json:"nodesExpected"`
	NodesResponded       int               `json:"nodesResponded"`
	NodesFailed          int               `json:"nodesFailed"`
	Warnings             []warningResponse `json:"warnings"`
}

// warningResponse is one node-level collection issue.
type warningResponse struct {
	Kind    string `json:"kind"`
	Code    string `json:"code"`
	Message string `json:"message"`
	NodeID  string `json:"nodeId,omitempty"`
}

// nodeRef is the node identity prefix embedded in node-scoped items.
type nodeRef struct {
	NodeID  string `json:"nodeId"`
	Shard   string `json:"shard"`
	Replica string `json:"replica"`
}

type nodeResponse struct {
	nodeRef
	Endpoint      string    `json:"endpoint"`
	Reachable     bool      `json:"reachable"`
	ObservedAt    time.Time `json:"observedAt"`
	Version       string    `json:"version,omitempty"`
	Timezone      string    `json:"timezone,omitempty"`
	UptimeSeconds string    `json:"uptimeSeconds,omitempty"`
	LastError     *string   `json:"lastError"`
}

type diskResponse struct {
	nodeRef
	Disk                   string  `json:"disk"`
	Type                   string  `json:"type"`
	ObjectStorageType      string  `json:"objectStorageType"`
	IsRemote               bool    `json:"isRemote"`
	IsBroken               bool    `json:"isBroken"`
	Path                   *string `json:"path"`
	CachePath              *string `json:"cachePath"`
	CapacityKnown          bool    `json:"capacityKnown"`
	FreeSpaceBytes         *string `json:"freeSpaceBytes"`
	TotalSpaceBytes        *string `json:"totalSpaceBytes"`
	UnreservedSpaceBytes   *string `json:"unreservedSpaceBytes"`
	UsedByActivePartsBytes string  `json:"usedByActivePartsBytes"`
}

// tableBase is the table identity and schema prefix shared by the table list
// and table detail responses.
type tableBase struct {
	Database      string  `json:"database"`
	Table         string  `json:"table"`
	Engine        string  `json:"engine"`
	StoragePolicy string  `json:"storagePolicy"`
	TargetDisk    string  `json:"targetDisk"`
	PartitionKey  string  `json:"partitionKey"`
	SortingKey    string  `json:"sortingKey"`
	PrimaryKey    string  `json:"primaryKey"`
	VersionColumn *string `json:"versionColumn"`
}

type tableSummaryResponse struct {
	tableBase
	NodesObserved       int                 `json:"nodesObserved"`
	ShardsObserved      int                 `json:"shardsObserved"`
	ReplicasPerShard    int                 `json:"replicasPerShard"`
	ActivePartitions    int                 `json:"activePartitions"`
	ActiveParts         string              `json:"activeParts"`
	Rows                string              `json:"rows"`
	BytesOnDisk         string              `json:"bytesOnDisk"`
	PartitionPlacements map[string]int      `json:"partitionPlacements"`
	PartitionOperations map[string]int      `json:"partitionOperations"`
	ActiveOperations    int                 `json:"activeOperations"`
	Conditions          []conditionResponse `json:"conditions"`
	Links               map[string]string   `json:"links"`
}

type tableDetailResponse struct {
	tableBase
	UUID                 string                   `json:"uuid"`
	SamplingKey          string                   `json:"samplingKey"`
	IsReplicated         bool                     `json:"isReplicated"`
	NodesObserved        int                      `json:"nodesObserved"`
	ActivePartitions     int                      `json:"activePartitions"`
	ActiveParts          string                   `json:"activeParts"`
	Rows                 string                   `json:"rows"`
	BytesOnDisk          string                   `json:"bytesOnDisk"`
	MinPartition         *string                  `json:"minPartition"`
	MaxPartition         *string                  `json:"maxPartition"`
	LastModificationTime *time.Time               `json:"lastModificationTime"`
	PartitionPlacements  map[string]int           `json:"partitionPlacements"`
	PartitionOperations  map[string]int           `json:"partitionOperations"`
	Nodes                []nodeTableStateResponse `json:"nodes"`
	Conditions           []conditionResponse      `json:"conditions"`
}

type nodeTableStateResponse struct {
	NodeID      string                `json:"nodeId"`
	Engine      string                `json:"engine"`
	ActiveParts string                `json:"activeParts"`
	Rows        string                `json:"rows"`
	BytesOnDisk string                `json:"bytesOnDisk"`
	Replica     *replicaStateResponse `json:"replica,omitempty"`
}

type replicaStateResponse struct {
	Readonly             bool   `json:"readonly"`
	SessionExpired       bool   `json:"sessionExpired"`
	QueueSize            string `json:"queueSize"`
	AbsoluteDelaySeconds string `json:"absoluteDelaySeconds"`
	TotalReplicas        string `json:"totalReplicas"`
	ActiveReplicas       string `json:"activeReplicas"`
}

type columnResponse struct {
	Name              string  `json:"name"`
	Position          uint64  `json:"position"`
	Type              string  `json:"type"`
	Kind              string  `json:"kind"`
	DefaultKind       *string `json:"defaultKind"`
	DefaultExpression *string `json:"defaultExpression"`
	CodecExpression   *string `json:"codecExpression"`
	TTLExpression     *string `json:"ttlExpression"`
	Comment           string  `json:"comment"`
	IsInPartitionKey  bool    `json:"isInPartitionKey"`
	IsInSortingKey    bool    `json:"isInSortingKey"`
	IsInPrimaryKey    bool    `json:"isInPrimaryKey"`
	IsInSamplingKey   bool    `json:"isInSamplingKey"`
}

type nodeColumnsResponse struct {
	nodeRef
	Columns    []columnResponse    `json:"columns"`
	Conditions []conditionResponse `json:"conditions"`
}

type partitionResponse struct {
	Database             string                       `json:"database"`
	Table                string                       `json:"table"`
	Partition            string                       `json:"partition"`
	PartitionID          string                       `json:"partitionId"`
	TargetDisk           string                       `json:"targetDisk"`
	Placement            string                       `json:"placement"`
	Operations           []string                     `json:"operations"`
	Disks                []string                     `json:"disks"`
	ActiveParts          string                       `json:"activeParts"`
	Rows                 string                       `json:"rows"`
	BytesOnDisk          string                       `json:"bytesOnDisk"`
	LastModificationTime *time.Time                   `json:"lastModificationTime"`
	Placements           []partitionPlacementResponse `json:"placements"`
	Conditions           []conditionResponse          `json:"conditions"`
}

type partitionPlacementResponse struct {
	nodeRef
	Disk                 string     `json:"disk"`
	ActiveParts          string     `json:"activeParts"`
	Rows                 string     `json:"rows"`
	BytesOnDisk          string     `json:"bytesOnDisk"`
	LastModificationTime *time.Time `json:"lastModificationTime"`
}

type partResponse struct {
	nodeRef
	Database                          string              `json:"database"`
	Table                             string              `json:"table"`
	Partition                         string              `json:"partition"`
	PartitionID                       string              `json:"partitionId"`
	PartName                          string              `json:"partName"`
	UUID                              string              `json:"uuid"`
	Active                            bool                `json:"active"`
	Disk                              string              `json:"disk"`
	Path                              string              `json:"path"`
	PartType                          string              `json:"partType"`
	Rows                              string              `json:"rows"`
	Marks                             string              `json:"marks"`
	BytesOnDisk                       string              `json:"bytesOnDisk"`
	DataCompressedBytes               string              `json:"dataCompressedBytes"`
	DataUncompressedBytes             string              `json:"dataUncompressedBytes"`
	MarksBytes                        string              `json:"marksBytes"`
	PrimaryKeyBytesInMemory           string              `json:"primaryKeyBytesInMemory"`
	PrimaryKeyBytesInMemoryAllocated  string              `json:"primaryKeyBytesInMemoryAllocated"`
	SecondaryIndicesCompressedBytes   string              `json:"secondaryIndicesCompressedBytes"`
	SecondaryIndicesUncompressedBytes string              `json:"secondaryIndicesUncompressedBytes"`
	SecondaryIndicesMarksBytes        string              `json:"secondaryIndicesMarksBytes"`
	ModificationTime                  time.Time           `json:"modificationTime"`
	RemoveTime                        *time.Time          `json:"removeTime"`
	Refcount                          string              `json:"refcount"`
	MinBlockNumber                    string              `json:"minBlockNumber"`
	MaxBlockNumber                    string              `json:"maxBlockNumber"`
	Level                             string              `json:"level"`
	DataVersion                       string              `json:"dataVersion"`
	DeleteTTLInfoMin                  *time.Time          `json:"deleteTtlInfoMin"`
	DeleteTTLInfoMax                  *time.Time          `json:"deleteTtlInfoMax"`
	MoveTTLInfo                       []map[string]any    `json:"moveTtlInfo"`
	RecompressionTTLInfo              []map[string]any    `json:"recompressionTtlInfo"`
	DefaultCompressionCodec           string              `json:"defaultCompressionCodec"`
	Conditions                        []conditionResponse `json:"conditions"`
}

type detachedPartResponse struct {
	nodeRef
	Database         string              `json:"database"`
	Table            string              `json:"table"`
	PartitionID      string              `json:"partitionId"`
	PartName         string              `json:"partName"`
	Disk             string              `json:"disk"`
	Reason           string              `json:"reason"`
	Path             string              `json:"path"`
	BytesOnDisk      string              `json:"bytesOnDisk"`
	Rows             string              `json:"rows"`
	MinBlockNumber   *string             `json:"minBlockNumber"`
	MaxBlockNumber   *string             `json:"maxBlockNumber"`
	Level            *string             `json:"level"`
	ModificationTime time.Time           `json:"modificationTime"`
	Conditions       []conditionResponse `json:"conditions"`
}

type operationResponse struct {
	OperationID    string     `json:"operationId"`
	Kind           string     `json:"kind"`
	NodeID         string     `json:"nodeId"`
	Database       string     `json:"database"`
	Table          string     `json:"table"`
	Partition      *string    `json:"partition"`
	PartitionID    *string    `json:"partitionId"`
	AttemptID      string     `json:"attemptId"`
	State          string     `json:"state"`
	ElapsedSeconds *float64   `json:"elapsedSeconds"`
	Progress       *float64   `json:"progress"`
	SourceDisk     *string    `json:"sourceDisk"`
	TargetDisk     *string    `json:"targetDisk"`
	BytesTotal     *string    `json:"bytesTotal"`
	BytesProcessed *string    `json:"bytesProcessed"`
	LatestMessage  *string    `json:"latestMessage"`
	StartedAt      *time.Time `json:"startedAt"`
}

type mutationResponse struct {
	nodeRef
	OperationID      string                        `json:"operationId"`
	Kind             string                        `json:"kind"`
	Database         string                        `json:"database"`
	Table            string                        `json:"table"`
	MutationID       string                        `json:"mutationId"`
	AttemptID        string                        `json:"attemptId"`
	Command          string                        `json:"command"`
	CreateTime       time.Time                     `json:"createTime"`
	IsDone           bool                          `json:"isDone"`
	IsKilled         bool                          `json:"isKilled"`
	PartsToDo        string                        `json:"partsToDo"`
	PartsToDoNames   []string                      `json:"partsToDoNames"`
	BlockNumbers     []mutationBlockNumberResponse `json:"blockNumbers"`
	LatestFailedPart *string                       `json:"latestFailedPart"`
	LatestFailTime   *time.Time                    `json:"latestFailTime"`
	LatestFailReason *string                       `json:"latestFailReason"`
	Conditions       []conditionResponse           `json:"conditions"`
}

type mutationBlockNumberResponse struct {
	PartitionID string `json:"partitionId"`
	Number      string `json:"number"`
}

type replicationQueueResponse struct {
	nodeRef
	OperationID          string              `json:"operationId"`
	Kind                 string              `json:"kind"`
	Database             string              `json:"database"`
	Table                string              `json:"table"`
	ReplicaName          string              `json:"replicaName"`
	Position             string              `json:"position"`
	NodeName             string              `json:"nodeName"`
	AttemptID            string              `json:"attemptId"`
	Type                 string              `json:"type"`
	CreateTime           time.Time           `json:"createTime"`
	RequiredQuorum       string              `json:"requiredQuorum"`
	SourceReplica        *string             `json:"sourceReplica"`
	NewPartName          *string             `json:"newPartName"`
	PartsToMerge         []string            `json:"partsToMerge"`
	IsDetach             bool                `json:"isDetach"`
	IsCurrentlyExecuting bool                `json:"isCurrentlyExecuting"`
	NumTries             string              `json:"numTries"`
	LastAttemptTime      *time.Time          `json:"lastAttemptTime"`
	LastPostponeTime     *time.Time          `json:"lastPostponeTime"`
	NumPostponed         string              `json:"numPostponed"`
	PostponeReason       *string             `json:"postponeReason"`
	LastException        *string             `json:"lastException"`
	Conditions           []conditionResponse `json:"conditions"`
}

type partEventResponse struct {
	nodeRef
	EventID           string    `json:"eventId"`
	Database          string    `json:"database"`
	Table             string    `json:"table"`
	PartitionID       string    `json:"partitionId"`
	PartName          string    `json:"partName"`
	EventType         string    `json:"eventType"`
	EventTime         time.Time `json:"eventTime"`
	DurationMs        string    `json:"durationMs"`
	Rows              string    `json:"rows"`
	BytesCompressed   string    `json:"bytesCompressed"`
	BytesUncompressed string    `json:"bytesUncompressed"`
	ReadRows          string    `json:"readRows"`
	ReadBytes         string    `json:"readBytes"`
	MergedFrom        []string  `json:"mergedFrom"`
	SourceDisk        *string   `json:"sourceDisk"`
	TargetDisk        *string   `json:"targetDisk"`
	Error             string    `json:"error"`
	Exception         *string   `json:"exception"`
}

type conditionResponse struct {
	ConditionID string            `json:"conditionId"`
	Severity    string            `json:"severity"`
	Code        string            `json:"code"`
	Message     string            `json:"message"`
	ObservedAt  time.Time         `json:"observedAt"`
	Database    *string           `json:"database"`
	Table       *string           `json:"table"`
	Partition   *string           `json:"partition"`
	PartitionID *string           `json:"partitionId"`
	NodeID      *string           `json:"nodeId"`
	Evidence    map[string]any    `json:"evidence"`
	Links       map[string]string `json:"links"`
}

// listResponse is the envelope for plain list endpoints.
type listResponse[T any] struct {
	Collection collectionResponse `json:"collection"`
	Items      []T                `json:"items"`
}

// tableScopedListResponse is the envelope for list endpoints scoped to one
// watched table.
type tableScopedListResponse[T any] struct {
	Collection collectionResponse `json:"collection"`
	Database   string             `json:"database"`
	Table      string             `json:"table"`
	Items      []T                `json:"items"`
}

type tableDetailEnvelope struct {
	Collection collectionResponse  `json:"collection"`
	Item       tableDetailResponse `json:"item"`
}

type columnsEnvelope struct {
	Collection collectionResponse    `json:"collection"`
	Database   string                `json:"database"`
	Table      string                `json:"table"`
	Items      []nodeColumnsResponse `json:"items"`
	Conditions []conditionResponse   `json:"conditions"`
}

type detachedPartsEnvelope struct {
	Collection collectionResponse     `json:"collection"`
	Database   string                 `json:"database"`
	Table      string                 `json:"table"`
	Items      []detachedPartResponse `json:"items"`
	Counts     detachedPartsCounts    `json:"counts"`
}

type detachedPartsCounts struct {
	Total    int            `json:"total"`
	ByReason map[string]int `json:"byReason"`
}

type operationsEnvelope struct {
	Collection collectionResponse  `json:"collection"`
	Items      []operationResponse `json:"items"`
	Counts     operationsCounts    `json:"counts"`
}

type operationsCounts struct {
	Total  int            `json:"total"`
	ByKind map[string]int `json:"byKind"`
}

type mutationsEnvelope struct {
	Collection collectionResponse `json:"collection"`
	Items      []mutationResponse `json:"items"`
	Counts     mutationsCounts    `json:"counts"`
}

type mutationsCounts struct {
	Total      int `json:"total"`
	Unfinished int `json:"unfinished"`
	Failed     int `json:"failed"`
}

type replicationQueueEnvelope struct {
	Collection collectionResponse         `json:"collection"`
	Items      []replicationQueueResponse `json:"items"`
	Counts     replicationQueueCounts     `json:"counts"`
}

type replicationQueueCounts struct {
	Total              int            `json:"total"`
	CurrentlyExecuting int            `json:"currentlyExecuting"`
	WithException      int            `json:"withException"`
	ByType             map[string]int `json:"byType"`
}

type partEventsEnvelope struct {
	Collection collectionResponse  `json:"collection"`
	Items      []partEventResponse `json:"items"`
	Counts     partEventsCounts    `json:"counts"`
}

type partEventsCounts struct {
	Total       int            `json:"total"`
	ByEventType map[string]int `json:"byEventType"`
	WithErrors  int            `json:"withErrors"`
}

type conditionsEnvelope struct {
	Collection collectionResponse  `json:"collection"`
	Items      []conditionResponse `json:"items"`
	Counts     conditionsCounts    `json:"counts"`
}

type conditionsCounts struct {
	Total      int            `json:"total"`
	BySeverity map[string]int `json:"bySeverity"`
}
