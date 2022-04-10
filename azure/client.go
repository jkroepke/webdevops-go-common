package azure

import (
	"context"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/resources/mgmt/resources"
	"github.com/Azure/azure-sdk-for-go/profiles/latest/resources/mgmt/subscriptions"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/Azure/go-autorest/autorest/to"
	cache "github.com/patrickmn/go-cache"
	log "github.com/sirupsen/logrus"

	"github.com/webdevops/go-common/prometheus/azuretracing"
)

type (
	Client struct {
		Environment azure.Environment
		Authorizer  autorest.Authorizer

		logger *log.Logger

		cache    *cache.Cache
		cacheTtl time.Duration

		userAgent string
	}
)

func NewClient(environment azure.Environment, authorizer autorest.Authorizer, logger *log.Logger) *Client {
	azureClient := &Client{}
	azureClient.Environment = environment
	azureClient.Authorizer = authorizer

	azureClient.cacheTtl = 30 * time.Minute
	azureClient.cache = cache.New(60*time.Minute, 60*time.Second)

	azureClient.logger = logger

	return azureClient
}

func NewClientFromEnvironment(environmentName string, logger *log.Logger) (*Client, error) {
	// azure authorizer
	authorizer, err := auth.NewAuthorizerFromEnvironment()
	if err != nil {
		return nil, err
	}

	environment, err := azure.EnvironmentFromName(environmentName)
	if err != nil {
		return nil, err
	}

	return NewClient(environment, authorizer, logger), nil
}

func (azureClient *Client) SetUserAgent(useragent string) {
	azureClient.userAgent = useragent
}

func (azureClient *Client) SetCacheTtl(ttl time.Duration) {
	azureClient.cacheTtl = ttl
}

func (azureClient *Client) DecorateAzureAutorest(client *autorest.Client) {
	azureClient.DecorateAzureAutorestWithAuthorizer(client, azureClient.Authorizer)
}

func (azureClient *Client) DecorateAzureAutorestWithAuthorizer(client *autorest.Client, authorizer autorest.Authorizer) {
	client.Authorizer = authorizer
	if azureClient.userAgent != "" {
		if err := client.AddToUserAgent(azureClient.userAgent); err != nil {
			panic(err)
		}
	}

	azuretracing.DecorateAzureAutoRestClient(client)
}

func (azureClient *Client) ListCachedSubscriptions(ctx context.Context) ([]subscriptions.Subscription, error) {
	cacheKey := "subscriptions"
	if v, ok := azureClient.cache.Get(cacheKey); ok {
		if cacheData, ok := v.([]subscriptions.Subscription); ok {
			return cacheData, nil
		}
	}

	azureClient.logger.Debug("updating cached Azure Subscription list")
	list, err := azureClient.ListSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	azureClient.logger.Debugf("found %v Azure Subscriptions", len(list))

	azureClient.cache.Set(cacheKey, list, azureClient.cacheTtl)

	return list, nil
}

func (azureClient *Client) ListSubscriptions(ctx context.Context) ([]subscriptions.Subscription, error) {
	list := []subscriptions.Subscription{}
	client := subscriptions.NewClientWithBaseURI(azureClient.Environment.ResourceManagerEndpoint)
	azureClient.DecorateAzureAutorest(&client.Client)

	result, err := client.ListComplete(ctx)
	if err != nil {
		return list, err
	}

	for result.NotDone() {
		row := result.Value()
		list = append(list, row)
		if result.NextWithContext(ctx) != nil {
			break
		}
	}

	return list, nil
}

func (azureClient *Client) ListCachedResourceGroups(ctx context.Context, subscription subscriptions.Subscription) (map[string]resources.Group, error) {
	list := map[string]resources.Group{}

	cacheKey := "resourcegroups:" + to.String(subscription.SubscriptionID)
	if v, ok := azureClient.cache.Get(cacheKey); ok {
		if cacheData, ok := v.(map[string]resources.Group); ok {
			return cacheData, nil
		}
	}

	azureClient.logger.WithField("subscriptionID", *subscription.SubscriptionID).Debug("updating cached Azure ResourceGroup list")
	list, err := azureClient.ListResourceGroups(ctx, subscription)
	if err != nil {
		return list, err
	}
	azureClient.logger.WithField("subscriptionID", *subscription.SubscriptionID).Debugf("found %v Azure ResourceGroups", len(list))

	azureClient.cache.Set(cacheKey, list, azureClient.cacheTtl)

	return list, nil
}

func (azureClient *Client) ListResourceGroups(ctx context.Context, subscription subscriptions.Subscription) (map[string]resources.Group, error) {
	list := map[string]resources.Group{}

	client := resources.NewGroupsClientWithBaseURI(azureClient.Environment.ResourceManagerEndpoint, *subscription.SubscriptionID)
	azureClient.DecorateAzureAutorest(&client.Client)

	result, err := client.ListComplete(ctx, "", nil)
	if err != nil {
		return list, err
	}

	for result.NotDone() {
		row := result.Value()

		resourceGroupName := strings.ToLower(to.String(row.Name))
		list[resourceGroupName] = row

		if result.NextWithContext(ctx) != nil {
			break
		}
	}

	return list, nil
}