package discord

type BotGatewayResponse struct {
	SessionStartLimit map[string]int `json:"session_start_limit"`
}

type BotUserResponse struct {
	Id string `json:"id"`
	Username string `json:"username"`
	Discrim string `json:"discriminator"`
}
