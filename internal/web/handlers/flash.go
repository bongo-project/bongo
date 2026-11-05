package handlers

import (
	"net/http"
	"strings"

	"github.com/bongo-project/bongo/internal/i18n"
	"github.com/bongo-project/bongo/internal/web/middleware"
)

// Flash banners are allow-listed keys, never request text. A redirect carries
// one or more wire keys (flash=user-created); a page renders only keys found
// in flashMessages, translated for the viewer. Unknown keys, free text, an
// i18n message ID or more than maxFlashKeys values render no banner, so a link
// someone else crafted cannot put its own words on a Bongo page.
//
// flashKey values are declared in this file only (the handler tests scan for
// any other writer of flash=). A message in flashMessages must contain no
// format verbs: nothing is ever interpolated into a flash.

// maxFlashKeys caps the keys one redirect may carry (a result plus notes).
const maxFlashKeys = 3

// flashKey is the wire form of one allow-listed flash message.
type flashKey struct{ id string }

// withFlash appends the flash keys to a path, composing with any parameters
// already on it (withInviteWarning, for one).
func withFlash(path string, keys ...flashKey) string {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, "flash="+k.id)
	}
	return appendParams(path, strings.Join(parts, "&"))
}

// carryFlash copies the request's allow-listed flash keys onto a redirect, for a
// page that redirects before anything renders. Anything else is dropped.
func carryFlash(path string, r *http.Request) string {
	vals := r.URL.Query()["flash"]
	if len(vals) > maxFlashKeys {
		return path
	}
	var keys []flashKey
	for _, v := range vals {
		if _, ok := flashMessages[v]; ok {
			keys = append(keys, flashKey{v})
		}
	}
	if len(keys) == 0 {
		return path
	}
	return withFlash(path, keys...)
}

// flashText returns the translated banner for the request's flash keys, or ""
// when there are none, too many, or any is not in the allow-list. Several keys
// read as sentences: a part not ending in punctuation gets a full stop.
func flashText(r *http.Request) string {
	vals := r.URL.Query()["flash"]
	if len(vals) == 0 || len(vals) > maxFlashKeys {
		return ""
	}
	locale := middleware.LocaleFromContext(r.Context())
	var b strings.Builder
	for i, v := range vals {
		msgID, ok := flashMessages[v]
		if !ok {
			return ""
		}
		text := i18n.T(locale, msgID)
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(text)
		if i < len(vals)-1 && !strings.HasSuffix(text, ".") && !strings.HasSuffix(text, "!") && !strings.HasSuffix(text, "?") {
			b.WriteByte('.')
		}
	}
	return b.String()
}

var (
	flashSent                        = flashKey{"sent"}
	flashSentLocalQueued             = flashKey{"sent-local-queued"}
	flashQueued                      = flashKey{"queued"}
	flashDraftSaved                  = flashKey{"draft-saved"}
	flashRuleCreated                 = flashKey{"rule-created"}
	flashRuleUpdated                 = flashKey{"rule-updated"}
	flashVacationSaved               = flashKey{"vacation-saved"}
	flashSendAsRequested             = flashKey{"send-as-requested"}
	flashFwdCodeSent                 = flashKey{"forward-code-sent"}
	flashFwdVerified                 = flashKey{"forward-verified"}
	flashFwdSavedPending             = flashKey{"forward-saved-pending"}
	flashSettingsSavedNote           = flashKey{"settings-saved-note"}
	flashFwdRuleVerify               = flashKey{"forward-rule-verify"}
	flashFwdRateLimited              = flashKey{"forward-rate-limited"}
	flashFwdUnavailable              = flashKey{"forward-unavailable"}
	flashFwdTooManyPending           = flashKey{"forward-too-many-pending"}
	flashArchived                    = flashKey{"archived"}
	flashUnarchived                  = flashKey{"unarchived"}
	flashQuotaExceeded               = flashKey{"quota-exceeded"}
	flashSettingsSaved               = flashKey{"settings-saved"}
	flashAddressAlreadyVerified      = flashKey{"address-already-verified"}
	flashVerificationCodeSent        = flashKey{"verification-code-sent"}
	flashAddressVerified             = flashKey{"address-verified"}
	flashResourceCreated             = flashKey{"resource-created"}
	flashBookingCancelled            = flashKey{"booking-cancelled"}
	flashUserCreated                 = flashKey{"user-created"}
	flashUserUpdated                 = flashKey{"user-updated"}
	flashUserEnabled                 = flashKey{"user-enabled"}
	flashUserDisabled                = flashKey{"user-disabled"}
	flashCannotDeleteSelf            = flashKey{"cannot-delete-self"}
	flashCannotDeleteLastAdmin       = flashKey{"cannot-delete-last-admin"}
	flashUserDeleted                 = flashKey{"user-deleted"}
	flashUserDeletedWithErrors       = flashKey{"user-deleted-with-errors"}
	flashDomainAddedMX               = flashKey{"domain-added-mx"}
	flashDomainAddedNoMX             = flashKey{"domain-added-no-mx"}
	flashDomainToggled               = flashKey{"domain-toggled"}
	flashDomainHasAliases            = flashKey{"domain-has-aliases"}
	flashDomainHasDependents         = flashKey{"domain-has-dependents"}
	flashDomainDeleted               = flashKey{"domain-deleted"}
	flashDKIMGenerated               = flashKey{"dkim-generated"}
	flashCatchAllSet                 = flashKey{"catchall-set"}
	flashCatchAllCleared             = flashKey{"catchall-cleared"}
	flashDomainOwnerSet              = flashKey{"owner-set"}
	flashDomainAdminGranted          = flashKey{"admin-granted"}
	flashDomainAdminRevoked          = flashKey{"admin-revoked"}
	flashDomainQuotaSet              = flashKey{"quota-set"}
	flashAliasCreated                = flashKey{"alias-created"}
	flashAliasDeleted                = flashKey{"alias-deleted"}
	flashQueueFlushed                = flashKey{"queue-flushed"}
	flashPolicy2FAUpdated            = flashKey{"policy-2fa-updated"}
	flashAddressAdded                = flashKey{"address-added"}
	flashCannotRemovePrimary         = flashKey{"cannot-remove-primary"}
	flashAddressRemoved              = flashKey{"address-removed"}
	flashSendGranted                 = flashKey{"send-granted"}
	flashEventCreated                = flashKey{"event-created"}
	flashEventUpdated                = flashKey{"event-updated"}
	flashEventDeleted                = flashKey{"event-deleted"}
	flashNoFileUploaded              = flashKey{"no-file-uploaded"}
	flashEventsImported              = flashKey{"events-imported"}
	flashEventsImportedSomeFailed    = flashKey{"events-imported-some-failed"}
	flashContactsImported            = flashKey{"contacts-imported"}
	flashContactsImportedSomeSkipped = flashKey{"contacts-imported-some-skipped"}
	flashTrashEmptied                = flashKey{"trash-emptied"}
	flashMessageMoved                = flashKey{"message-moved"}
	flashMessageTrashed              = flashKey{"message-trashed"}
	flashMessageDeleted              = flashKey{"message-deleted"}
	flashMarkedSpam                  = flashKey{"marked-spam"}
	flashMarkedNotSpam               = flashKey{"marked-not-spam"}
	flashTrainingSkipped             = flashKey{"training-skipped"}
	flashContactCreated              = flashKey{"contact-created"}
	flashContactUpdated              = flashKey{"contact-updated"}
	flashContactDeleted              = flashKey{"contact-deleted"}
	flashAlreadyInContacts           = flashKey{"already-in-contacts"}
	flashContactAdded                = flashKey{"contact-added"}
	flashFileReadFailed              = flashKey{"file-read-failed"}
	flashQuarantineReleased          = flashKey{"quarantine-message-released"}
	flashQuarantineDeleted           = flashKey{"quarantine-message-deleted"}
	flashQuarantineNoneSelected      = flashKey{"quarantine-none-selected"}
	flashQuarantineBulkReleased      = flashKey{"quarantine-released"}
	flashQuarantineBulkDeleted       = flashKey{"quarantine-deleted"}
	flashQuarantineBulkRescanned     = flashKey{"quarantine-rescanned"}
	flashQuarantineBulkProcessed     = flashKey{"quarantine-processed"}
	flashQuarantineSomeFailed        = flashKey{"quarantine-some-failed"}
	flashSecuritySetupExpired        = flashKey{"security-setup-expired"}
	flashSecurityInvalidCode         = flashKey{"security-invalid-code"}
	flashSecurityPasswordRequired    = flashKey{"security-password-required"}
	flashSecurity2FADisabled         = flashKey{"security-2fa-disabled"}
	flashSecurityFieldsRequired      = flashKey{"security-fields-required"}
	flashSecurityPasswordsMismatch   = flashKey{"security-passwords-mismatch"}
	flashSecurityPasswordShort       = flashKey{"security-password-short"}
	flashSecurityPasswordSame        = flashKey{"security-password-same"}
	flashSecurityPasswordChanged     = flashKey{"security-password-changed"}
	flashSecurity2FAAlreadyEnabled   = flashKey{"security-2fa-already-enabled"}
	flashSecurityInvalidPassword     = flashKey{"security-invalid-password"}
	flashSecurityInvalidAuthCode     = flashKey{"security-invalid-auth-code"}
	flashRuleDeleted                 = flashKey{"rule-deleted"}
	flashAppPasswordRevoked          = flashKey{"app-password-revoked"}
	flashAppPasswordsRevokedAll      = flashKey{"app-passwords-revoked-all"}
)

var flashMessages = map[string]string{
	flashSent.id:                        "compose.flashSent",
	flashSentLocalQueued.id:             "compose.flashSentLocalQueued",
	flashQueued.id:                      "compose.flashQueued",
	flashDraftSaved.id:                  "compose.flashDraftSaved",
	flashRuleCreated.id:                 "rules.flashCreated",
	flashRuleUpdated.id:                 "rules.flashUpdated",
	flashVacationSaved.id:               "settings.vacationSaved",
	flashSendAsRequested.id:             "settings.sendAsRequested",
	flashFwdCodeSent.id:                 "settings.fwdCodeSent",
	flashFwdVerified.id:                 "settings.fwdVerified",
	flashFwdSavedPending.id:             "settings.fwdSavedPending",
	flashSettingsSavedNote.id:           "settings.fwdSavedPrefix",
	flashFwdRuleVerify.id:               "settings.fwdRuleVerify",
	flashFwdRateLimited.id:              "settings.fwdRateLimited",
	flashFwdUnavailable.id:              "settings.fwdUnavailable",
	flashFwdTooManyPending.id:           "settings.fwdTooManyPending",
	flashArchived.id:                    "mail.archived",
	flashUnarchived.id:                  "mail.unarchived",
	flashQuotaExceeded.id:               "quota.exceeded",
	flashSettingsSaved.id:               "flash.settingsSaved",
	flashAddressAlreadyVerified.id:      "flash.addressAlreadyVerified",
	flashVerificationCodeSent.id:        "flash.verificationCodeSent",
	flashAddressVerified.id:             "flash.addressVerified",
	flashResourceCreated.id:             "flash.resourceCreated",
	flashBookingCancelled.id:            "flash.bookingCancelled",
	flashUserCreated.id:                 "flash.userCreated",
	flashUserUpdated.id:                 "flash.userUpdated",
	flashUserEnabled.id:                 "flash.userEnabled",
	flashUserDisabled.id:                "flash.userDisabled",
	flashCannotDeleteSelf.id:            "flash.cannotDeleteSelf",
	flashCannotDeleteLastAdmin.id:       "flash.cannotDeleteLastAdmin",
	flashUserDeleted.id:                 "flash.userDeleted",
	flashUserDeletedWithErrors.id:       "flash.userDeletedWithErrors",
	flashDomainAddedMX.id:               "flash.domainAddedMx",
	flashDomainAddedNoMX.id:             "flash.domainAddedNoMx",
	flashDomainToggled.id:               "flash.domainToggled",
	flashDomainHasAliases.id:            "flash.domainHasAliases",
	flashDomainHasDependents.id:         "flash.domainHasDependents",
	flashDomainDeleted.id:               "flash.domainDeleted",
	flashDKIMGenerated.id:               "flash.dkimGenerated",
	flashCatchAllSet.id:                 "flash.catchallSet",
	flashCatchAllCleared.id:             "flash.catchallCleared",
	flashDomainOwnerSet.id:              "flash.ownerSet",
	flashDomainAdminGranted.id:          "flash.adminGranted",
	flashDomainAdminRevoked.id:          "flash.adminRevoked",
	flashDomainQuotaSet.id:              "flash.quotaSet",
	flashAliasCreated.id:                "flash.aliasCreated",
	flashAliasDeleted.id:                "flash.aliasDeleted",
	flashQueueFlushed.id:                "flash.queueFlushed",
	flashPolicy2FAUpdated.id:            "flash.policy2faUpdated",
	flashAddressAdded.id:                "flash.addressAdded",
	flashCannotRemovePrimary.id:         "flash.cannotRemovePrimary",
	flashAddressRemoved.id:              "flash.addressRemoved",
	flashSendGranted.id:                 "flash.sendGranted",
	flashEventCreated.id:                "flash.eventCreated",
	flashEventUpdated.id:                "flash.eventUpdated",
	flashEventDeleted.id:                "flash.eventDeleted",
	flashNoFileUploaded.id:              "flash.noFileUploaded",
	flashEventsImported.id:              "flash.eventsImported",
	flashEventsImportedSomeFailed.id:    "flash.eventsImportedSomeFailed",
	flashContactsImported.id:            "flash.contactsImported",
	flashContactsImportedSomeSkipped.id: "flash.contactsImportedSomeSkipped",
	flashTrashEmptied.id:                "flash.trashEmptied",
	flashMessageMoved.id:                "flash.messageMoved",
	flashMessageTrashed.id:              "flash.messageTrashed",
	flashMessageDeleted.id:              "flash.messageDeleted",
	flashMarkedSpam.id:                  "flash.markedSpam",
	flashMarkedNotSpam.id:               "flash.markedNotSpam",
	flashTrainingSkipped.id:             "flash.trainingSkipped",
	flashContactCreated.id:              "flash.contactCreated",
	flashContactUpdated.id:              "flash.contactUpdated",
	flashContactDeleted.id:              "flash.contactDeleted",
	flashAlreadyInContacts.id:           "flash.alreadyInContacts",
	flashContactAdded.id:                "flash.contactAdded",
	flashFileReadFailed.id:              "flash.fileReadFailed",
	flashQuarantineReleased.id:          "flash.quarantineMessageReleased",
	flashQuarantineDeleted.id:           "flash.quarantineMessageDeleted",
	flashQuarantineNoneSelected.id:      "flash.quarantineNoneSelected",
	flashQuarantineBulkReleased.id:      "flash.quarantineReleased",
	flashQuarantineBulkDeleted.id:       "flash.quarantineDeleted",
	flashQuarantineBulkRescanned.id:     "flash.quarantineRescanned",
	flashQuarantineBulkProcessed.id:     "flash.quarantineProcessed",
	flashQuarantineSomeFailed.id:        "flash.quarantineSomeFailed",
	flashSecuritySetupExpired.id:        "flash.securitySetupExpired",
	flashSecurityInvalidCode.id:         "flash.securityInvalidCode",
	flashSecurityPasswordRequired.id:    "flash.securityPasswordRequired",
	flashSecurity2FADisabled.id:         "flash.security2faDisabled",
	flashSecurityFieldsRequired.id:      "flash.securityFieldsRequired",
	flashSecurityPasswordsMismatch.id:   "flash.securityPasswordsMismatch",
	flashSecurityPasswordShort.id:       "flash.securityPasswordShort",
	flashSecurityPasswordSame.id:        "flash.securityPasswordSame",
	flashSecurityPasswordChanged.id:     "flash.securityPasswordChanged",
	flashSecurity2FAAlreadyEnabled.id:   "flash.security2faAlreadyEnabled",
	flashSecurityInvalidPassword.id:     "flash.securityInvalidPassword",
	flashSecurityInvalidAuthCode.id:     "flash.securityInvalidAuthCode",
	flashRuleDeleted.id:                 "flash.ruleDeleted",
	flashAppPasswordRevoked.id:          "flash.appPasswordRevoked",
	flashAppPasswordsRevokedAll.id:      "flash.appPasswordsRevokedAll",
}
