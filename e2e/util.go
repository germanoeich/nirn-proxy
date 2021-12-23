package e2e

import (
	"github.com/bwmarrin/snowflake"
	"math/rand"
	"strconv"
	"strings"
)

var node, _ = snowflake.NewNode(1)

var routes = []string{
	"/api/v9/guilds/!/audit-logs",
	"/api/v9/guilds/!/members/!",
	"/api/v9/channels/!/messages",
	"/api/v9/channels/!",
	"/api/v9/interactions/!/aW50ZXJhY3Rpb246OTE1ODAxMzMwMzQ4NjAxNDA1OkNoQml5bXJ3TUw5WGNIN2NSdFRMNlVAHFGWm1EUUVTSW84a3ZIY0FyQzRpRFQ4YUVqOXFpR09Idkd4Y3Fsc09kblFDbzQyZEh5cmJTblZwYXd1eXZqbmFVOURyVk5ScDNWODhOVEx2dnVObXVJZzQzaW5Rd3ZFa0JVdTFvdXBB",
	"/api/v9/users/!/channels",
	"/api/v9/guilds/!/channels",
	"/api/v9/guilds/!/members/!/roles/!",
	"/api/v9/channels/!/messages/!",
	"/api/v9/guilds/!/bans/!",
	"/api/v9/guilds/!/bans",
	"/api/v9/guilds/!/webhooks",
	"/api/v9/users/!",
	"/api/v9/webhooks/!",
	"/api/v9/guilds/!/roles/!",
	"/api/v9/guilds/!/invites",
}

func GenerateSnowflake() int64 {
	return int64(node.Generate())
}

func GenerateSnowflakeStr() string {
	return strconv.FormatInt(GenerateSnowflake(), 10)
}

func GetRandomRoute() string {
	return strings.ReplaceAll(routes[rand.Intn(len(routes))], "!", GenerateSnowflakeStr())
}