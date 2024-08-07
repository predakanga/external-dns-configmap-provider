package pkg

import (
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"net/http"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	"sigs.k8s.io/external-dns/provider/webhook/api"
	"slices"
)

type Provider struct {
	domainFilter   endpoint.DomainFilter
	storage        Storage
	allowWildcards bool
	*gin.Engine
}

func NewProvider(domainFilter endpoint.DomainFilter, storage Storage, allowWildcards bool) *Provider {
	p := &Provider{
		domainFilter,
		storage,
		allowWildcards,
		gin.Default(),
	}
	p.configureRoutes()

	return p
}

func (p *Provider) configureRoutes() {
	p.GET("/healthz", p.getHealth)
	p.GET("/", p.getDomainFilter)
	p.GET("/records", p.getRecords)
	p.POST("/records", p.changeRecords)
	p.POST("/adjustendpoints", p.takeAdjust)
}

func (p *Provider) getHealth(c *gin.Context) {
	c.String(http.StatusOK, "OK")
}

func (p *Provider) getDomainFilter(c *gin.Context) {
	c.Header(api.ContentTypeHeader, api.MediaTypeFormatAndVersion)
	c.JSON(http.StatusOK, p.domainFilter)
}

func (p *Provider) getRecords(c *gin.Context) {
	if records, err := p.storage.Load(c); err != nil {
		_ = c.AbortWithError(http.StatusInternalServerError, err)
	} else {
		c.Header(api.ContentTypeHeader, api.MediaTypeFormatAndVersion)
		c.JSON(http.StatusOK, records)
	}
}

func (p *Provider) changeRecords(c *gin.Context) {
	var changes plan.Changes
	if err := c.BindJSON(&changes); err != nil {
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	log.Debugf("Received plan: %+v", changes)
	newRecords, err := p.storage.Load(c)
	if err != nil {
		_ = c.AbortWithError(http.StatusInternalServerError, err)
	}
	for _, ep := range changes.Delete {
		newRecords = slices.DeleteFunc(newRecords, func(e *endpoint.Endpoint) bool {
			return e.DNSName == ep.DNSName && e.SetIdentifier == ep.SetIdentifier
		})
	}
	for _, ep := range changes.UpdateOld {
		newRecords = slices.DeleteFunc(changes.UpdateOld, func(e *endpoint.Endpoint) bool {
			return e.DNSName == ep.DNSName && e.SetIdentifier == ep.SetIdentifier
		})
	}
	for _, ep := range changes.UpdateNew {
		newRecords = append(newRecords, ep)
	}
	for _, ep := range changes.Create {
		newRecords = append(newRecords, ep)
	}
	log.Debugf("New records: %+v", newRecords)

	if err := p.storage.Save(c, newRecords); err != nil {
		_ = c.AbortWithError(http.StatusInternalServerError, err)
	} else {
		c.Header(api.ContentTypeHeader, api.MediaTypeFormatAndVersion)
		c.Status(http.StatusNoContent)
	}
}

// Called by the consumer to canonicalize endpoints
// The only change we make is potentially stripping out wildcard entries
func (p *Provider) takeAdjust(c *gin.Context) {
	var desiredEndpoints []*endpoint.Endpoint
	if err := c.BindJSON(&desiredEndpoints); err != nil {
		_ = c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	log.Debugf("Pre-adjust endpoints: %+v", desiredEndpoints)
	finalEndpoints := make([]*endpoint.Endpoint, 0, len(desiredEndpoints))
	for _, ep := range desiredEndpoints {
		if ep.DNSName[0] == '*' && !p.allowWildcards {
			continue
		}
		finalEndpoints = append(finalEndpoints, ep)
	}
	log.Debugf("Post-adjust endpoints: %+v", finalEndpoints)

	c.Header(api.ContentTypeHeader, api.MediaTypeFormatAndVersion)
	c.JSON(http.StatusOK, finalEndpoints[:])
}
