package ctr

const (
	SvcTitleUtilities   = 12
	SvcAsyncMatchMaking = 145 // bdAsyncMatchMaking
	SvcHTTPProxy        = 194 // bdABTesting, via bdHTTPProxyRequest/Response

	// Numbers AND result types read from the bdTaskParams of each bdAsyncMatchMaking
	// function. bdUInt64Result::deserialize = readUInt64, bdStringResult = readString,
	// bdBoolResult = readBool, bdLobbyDocuments = two strings.
	TaskSetPlayerInfo      = 2  // no result
	TaskGetPlayerToken     = 3  // bdUInt64Result
	TaskQoSHostsReply      = 4  // no result
	TaskGetMMStatus        = 5  // bdStringResult
	TaskInitMatchMaking    = 6  // bdStringResult
	TaskStartMatchMaking   = 7  // bdStringResult
	TaskLobbyDisbanded     = 10 // no result
	TaskGetLobbyDocuments  = 13 // bdLobbyDocuments
	TaskAckExpectGame      = 14 // no result
	TaskSyncLobbyDocuments = 15 // bdBoolResult
	TaskInitiateDCQoS      = 17 // bdStringResult
	TaskStartSearch        = 18 // bdStringResult

	TaskGetServerTime = 6
)
