package pkg

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/external-dns/endpoint"
	"slices"
	"strings"
	"text/template"
)

const configTpl = `
{%- with .standard -%}
hosts {
{%- range . %}
	{% index .Targets 0 %} {% .DNSName %}
{%- end %}

	ttl 60
	no_reverse
	fallthrough
}
{%- end %}

{% range $record := .wildcard -%}
template IN {% .RecordType %} {% slice .DNSName 2 %} {
	answer "{{ .Name }} {% or .RecordTTL 60 %} IN {% .RecordType %} {% index .Targets 0 %}"
	{%- range slice .Targets 1 %}
	additional "{{ .Name }} {% or $record.RecordTTL 60 %} IN {% $record.RecordType %} {% . %}"
	{%- end %}

	fallthrough
}
{% end %}
`

type Storage struct {
	name, namespace string
	kubeConfig      *rest.Config
	configTemplate  *template.Template
}

func NewStorage(name, namespace, configPath string) Storage {
	// Set up the kubernetes config once at startup
	// TODO: Use a cache/watcher to minimize roundtrips
	config, err := clientcmd.BuildConfigFromFlags("", configPath)
	if err != nil {
		log.WithError(err).Fatal("Could not load kubeconfig")
	}

	// Use custom delimiters for our template because the DNS responses use the standard ones
	tpl := template.New("config").Delims("{%", "%}")
	if _, err := tpl.Parse(configTpl); err != nil {
		log.WithError(err).Fatal("Could not parse config template")
	}

	toRet := Storage{
		name,
		namespace,
		config,
		tpl,
	}

	// Do an initial load and save to canonicalize the config
	records, err := toRet.Load(context.Background())
	if err != nil {
		log.WithError(err).Fatal("Loading ConfigMap failed")
	}
	if err := toRet.Save(context.Background(), records); err != nil {
		log.WithError(err).Fatal("Saving ConfigMap failed")
	}

	return toRet
}

func (s Storage) client() (*kubernetes.Clientset, error) {
	return kubernetes.NewForConfig(s.kubeConfig)
}

func (s Storage) Load(ctx context.Context) ([]*endpoint.Endpoint, error) {
	c, err := s.client()
	if err != nil {
		return nil, errors.Wrap(err, "Could not connect to kubernetes")
	}
	cm, err := c.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Wrap(err, "Could not fetch configmap")
	}
	data, ok := cm.Data["records"]
	if !ok {
		return nil, errors.Wrap(err, "Malformed configmap (missing records key)")
	}
	var records []*endpoint.Endpoint
	if err := json.Unmarshal([]byte(data), &records); err != nil {
		return nil, errors.Wrap(err, "Unmarshalling records failed")
	}

	return records, nil
}

func (s Storage) emptyConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.name,
			Namespace: s.namespace,
		},
		Data: map[string]string{"records": "[]", "config": ""},
	}
}

func (s Storage) Save(ctx context.Context, newRecords []*endpoint.Endpoint) error {
	config, err := s.renderConfig(newRecords)
	if err != nil {
		return errors.Wrap(err, "Rendering config failed")
	}
	data, err := json.Marshal(newRecords)
	if err != nil {
		return errors.Wrap(err, "Marshalling records failed")
	}
	c, err := s.client()
	if err != nil {
		return errors.Wrap(err, "Could not connect to kubernetes")
	}
	cm, err := c.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		cm, err = c.CoreV1().ConfigMaps(s.namespace).Create(ctx, s.emptyConfigMap(), metav1.CreateOptions{})
	}
	if err != nil {
		return errors.Wrap(err, "Could not fetch or create configmap")
	}
	cm.Data["records"] = string(data)
	cm.Data["config"] = config
	// TODO: Don't update if there have been no changes
	if _, err := c.CoreV1().ConfigMaps(s.namespace).Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
		return errors.Wrap(err, "Could not update configmap")
	}
	return nil
}

func (s Storage) renderConfig(records []*endpoint.Endpoint) (string, error) {
	// TODO: Support per-record TTLs
	// TODO: Support multiple IPs for standard records
	// TODO: Support non-A records

	// Sort the records, for readability
	slices.SortFunc(records, func(a, b *endpoint.Endpoint) int {
		return strings.Compare(a.DNSName, b.DNSName)
	})

	// To simplify the template, split records into wildcard and standard
	standard := make([]*endpoint.Endpoint, 0, len(records))
	wildcard := make([]*endpoint.Endpoint, 0, len(records))

	for _, ep := range records {
		if ep.DNSName[0] != '*' {
			if ep.RecordType != "A" {
				log.Warnf("Record \"%s\" uses unsupported record type \"%s\". Skipping.", ep.DNSName, ep.RecordType)
				continue
			}
			if ep.RecordTTL.IsConfigured() {
				log.Warnf("Record \"%s\" uses unsupported custom TTL \"%d\". Defaulting to 60s.", ep.DNSName, ep.RecordTTL)
			}
			standard = append(standard, ep)
		} else {
			wildcard = append(wildcard, ep)
		}
	}

	ctx := map[string][]*endpoint.Endpoint{
		"standard": standard[:],
		"wildcard": wildcard[:],
	}
	buf := bytes.Buffer{}

	if err := s.configTemplate.Execute(&buf, ctx); err != nil {
		return "", err
	}

	return buf.String(), nil
}
