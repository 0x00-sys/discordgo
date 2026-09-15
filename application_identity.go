package discordgo

// ApplicationIdentity identifies a user's external account for an application.
// https://docs.discord.com/developers/resources/application-identity-profile#application-identity-object
type ApplicationIdentity struct {
	UserID               string `json:"user_id,omitempty"`
	ProviderType         string `json:"provider_type"`
	ProviderID           string `json:"provider_id,omitempty"`
	ProviderIssuedUserID string `json:"provider_issued_user_id"`
}

// ApplicationIdentityProfile stores game data on an application identity.
type ApplicationIdentityProfile struct {
	Username *string                         `json:"username"`
	Metadata map[string]interface{}          `json:"metadata"`
	Data     *ApplicationIdentityProfileData `json:"data"`
}

// ApplicationIdentityProfileParams contains the fields to update on a profile.
type ApplicationIdentityProfileParams struct {
	Username *string `json:"username,omitempty"`
	// Data fully replaces the existing data when provided. Nil leaves it unchanged.
	Data *ApplicationIdentityProfileData `json:"data,omitempty"`
}

// ApplicationIdentityProfileData contains pre-configured and custom game stats.
type ApplicationIdentityProfileData struct {
	Primary *ApplicationIdentityProfilePrimaryData    `json:"primary,omitempty"`
	Dynamic []*ApplicationIdentityProfileDynamicField `json:"dynamic,omitempty"`
}

// ApplicationIdentityProfilePrimaryData contains optional pre-configured game stats.
type ApplicationIdentityProfilePrimaryData struct {
	Season                       *string                          `json:"season,omitempty"`
	RankName                     *string                          `json:"rank_name,omitempty"`
	RankImage                    *ApplicationIdentityProfileMedia `json:"rank_image,omitempty"`
	HighestRank                  *string                          `json:"highest_rank,omitempty"`
	HighestRankImage             *ApplicationIdentityProfileMedia `json:"highest_rank_image,omitempty"`
	FeaturedPlayedCharacter      *string                          `json:"featured_played_character,omitempty"`
	FeaturedPlayedCharacterImage *ApplicationIdentityProfileMedia `json:"featured_played_character_image,omitempty"`
	PlaytimeHours                *float64                         `json:"playtime_hours,omitempty"`
	TotalWins                    *int                             `json:"total_wins,omitempty"`
	CurrentPeriodWins            *int                             `json:"current_period_wins,omitempty"`
	TotalGames                   *int                             `json:"total_games,omitempty"`
	CurrentPeriodGames           *int                             `json:"current_period_games,omitempty"`
	TotalKills                   *int                             `json:"total_kills,omitempty"`
	CurrentPeriodKills           *int                             `json:"current_period_kills,omitempty"`
	TotalAssists                 *int                             `json:"total_assists,omitempty"`
	CurrentPeriodAssists         *int                             `json:"current_period_assists,omitempty"`
	TotalDeaths                  *int                             `json:"total_deaths,omitempty"`
	CurrentPeriodDeaths          *int                             `json:"current_period_deaths,omitempty"`
}

// ApplicationIdentityProfileDynamicFieldType identifies a custom game stat's value type.
type ApplicationIdentityProfileDynamicFieldType int

// Supported application identity profile dynamic field types.
const (
	ApplicationIdentityProfileDynamicFieldString ApplicationIdentityProfileDynamicFieldType = 1
	ApplicationIdentityProfileDynamicFieldNumber ApplicationIdentityProfileDynamicFieldType = 2
	ApplicationIdentityProfileDynamicFieldMedia  ApplicationIdentityProfileDynamicFieldType = 3
)

// ApplicationIdentityProfileDynamicField contains a custom game stat.
type ApplicationIdentityProfileDynamicField struct {
	Type ApplicationIdentityProfileDynamicFieldType `json:"type"`
	Name string                                     `json:"name"`
	// Value is a string, number, or ApplicationIdentityProfileMedia according to Type.
	// JSON decoding represents numbers as float64 and media as map[string]interface{}.
	Value interface{} `json:"value"`
}

// ApplicationIdentityProfileMedia references a publicly accessible media asset.
type ApplicationIdentityProfileMedia struct {
	URL string `json:"url"`
}
