package agent

import (
	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/pipeline/attachpolicy"
	"github.com/bongo-project/bongo/internal/store"
)

// attachmentPolicyDeps builds the collaborators of the attachment policy stage.
// With no Pebble store or blob store the stage passes mail unchecked and says
// so once.
func (a *Agent) attachmentPolicyDeps(resolver *store.RecipientResolver) attachpolicy.Deps {
	d := attachpolicy.Deps{
		Logger:  a.logger,
		MaxSize: a.Config.SMTP.Limits.MaxMessageSizeBytes(),
		Translate: func(locale, id string, data map[string]interface{}) string {
			return i18n.Global().T(locale, id, data)
		},
	}
	if a.AttachmentPolicy == nil || a.Store == nil || a.Blobs == nil {
		return d
	}
	d.Policies = a.AttachmentPolicy
	d.Blobs = a.Blobs
	if resolver != nil {
		d.Resolve = resolver.ResolveRecipient
	}
	d.UserDomain = (&store.UserDomains{Pebble: a.Store}).UserDomain
	d.PolicyDomain = func(domain string) (string, bool, error) {
		hosted, err := store.DomainHosted(a.Store, domain)
		if err != nil || !hosted {
			return domain, hosted, err
		}
		target, isAlias, err := store.DomainAliasOf(a.Store, domain)
		if err != nil {
			return domain, true, err
		}
		if isAlias {
			return target, true, nil
		}
		return domain, true, nil
	}
	return d
}

// buildAttachmentPolicyStage returns the attachment policy stage.
func (a *Agent) buildAttachmentPolicyStage(resolver *store.RecipientResolver) *attachpolicy.Processor {
	return attachpolicy.New(a.attachmentPolicyDeps(resolver))
}
