/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2006 Novell, Inc. All Rights Reserved.
 * 
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2 of the GNU General Public License
 * as published by the Free Software Foundation.
 * 
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 * 
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, contact Novell, Inc.
 * 
 * To contact Novell about this file by physical or electronic mail, you 
 * may find current contact information at www.novell.com.
 * </Novell-copyright>
 ****************************************************************************/

#include <config.h>
#include <xpl.h>
#include <bongocal.h>
#include <bongocal-raw.h>
#include <ical.h>
#include <bongojson.h>
#include <bongoutil.h>

#define DEBUG_BONGOCAL 0

#if DEBUG_BONGOCAL
#define DPRINT(...) printf(__VA_ARGS__)
#else
#define DPRINT(...)
#endif

typedef void (*IcalRecurPropertyToJson) (char *key, char *value, BongoJsonObject *obj);
typedef void (*JsonRecurPropertyToIcal) (char *key, BongoJsonNode *node, BongoStringBuilder *sb);

typedef void (*IcalValueToJson) (icalproperty *prop, BongoJsonObject *object, icalvalue_kind kind);
typedef void (*JsonValueToIcal) (BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind);

typedef struct {
    icalproperty_kind icalKind;
    const char *jsonName;
    BOOL multiValue;
} PropertyType;

typedef struct {
    BongoCalType calType;
    icalcomponent_kind icalKind;
    const char *jsonName;
    PropertyType *propTypes;
    int numTypes;
} ComponentType;

typedef struct {
    icalvalue_kind icalKind;
    IcalValueToJson toJson;
    JsonValueToIcal toIcal;
} ValueType;

typedef struct {
    char *icalKey;
    char *jsonKey;
    BongoJsonType jsonType;
    IcalRecurPropertyToJson toJson;
    JsonRecurPropertyToIcal toIcal;
} RecurProperty;


static void IcalRecurPropertyToJsonKeyword(char *key, char *value, BongoJsonObject *obj);
static void IcalRecurPropertyToJsonString(char *key, char *value, BongoJsonObject *obj);
static void IcalRecurPropertyToJsonInt(char *key, char *value, BongoJsonObject *obj);
static void IcalRecurPropertyToJsonKeywordArray(char *key, char *value, BongoJsonObject *obj);
static void IcalRecurPropertyToJsonIntArray(char *key, char *value, BongoJsonObject *obj);

static void JsonRecurPropertyToIcalKeyword(char *key, BongoJsonNode *value, BongoStringBuilder *sb);
static void JsonRecurPropertyToIcalString(char *key, BongoJsonNode *value, BongoStringBuilder *sb);
static void JsonRecurPropertyToIcalInt(char *key, BongoJsonNode *value, BongoStringBuilder *sb);
static void JsonRecurPropertyToIcalKeywordArray(char *key, BongoJsonNode *value, BongoStringBuilder *sb);
static void JsonRecurPropertyToIcalIntArray(char *key, BongoJsonNode *value, BongoStringBuilder *sb);
    
static void IcalValueToJsonText(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind);
static void IcalValueToJsonString(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind);
static void IcalValueToJsonBool(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind);
static void IcalValueToJsonDate(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind);
static void IcalValueToJsonDateTime(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind);
static void IcalValueToJsonDateTimePeriod(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind);
static void IcalValueToJsonDuration(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind);
static void IcalValueToJsonFloat(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind);
static void IcalValueToJsonInt(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind);
static void IcalValueToJsonPeriod(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind);
static void IcalValueToJsonRecur(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind);
static void IcalValueToJsonTime(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind);
static void IcalValueToJsonGeo(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind);

static void JsonValueToIcalText(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind);
static void JsonValueToIcalString(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind);
static void JsonValueToIcalBool(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind);
static void JsonValueToIcalDate(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind);
static void JsonValueToIcalDateTime(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind);
static void JsonValueToIcalDateTimePeriod(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind);
static void JsonValueToIcalDuration(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind);
static void JsonValueToIcalFloat(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind);
static void JsonValueToIcalInt(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind);
static void JsonValueToIcalPeriod(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind);
static void JsonValueToIcalRecur(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind);
static void JsonValueToIcalTime(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind);
static void JsonValueToIcalGeo(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind);

RecurProperty recurProperties[] = {
    { "FREQ", "freq", BONGO_JSON_STRING, IcalRecurPropertyToJsonKeyword, JsonRecurPropertyToIcalKeyword },
    { "UNTIL", "until", BONGO_JSON_STRING, IcalRecurPropertyToJsonString, JsonRecurPropertyToIcalString },
    { "COUNT", "count", BONGO_JSON_INT, IcalRecurPropertyToJsonInt, JsonRecurPropertyToIcalInt },
    { "INTERVAL", "interval", BONGO_JSON_INT, IcalRecurPropertyToJsonInt, JsonRecurPropertyToIcalInt },
    { "BYSECOND", "bysecond", BONGO_JSON_ARRAY, IcalRecurPropertyToJsonIntArray, JsonRecurPropertyToIcalIntArray },
    { "BYMINUTE", "byminute", BONGO_JSON_ARRAY, IcalRecurPropertyToJsonIntArray, JsonRecurPropertyToIcalIntArray },
    { "BYHOUR", "byhour", BONGO_JSON_ARRAY, IcalRecurPropertyToJsonIntArray, JsonRecurPropertyToIcalIntArray },
    { "BYDAY", "byday", BONGO_JSON_ARRAY, IcalRecurPropertyToJsonKeywordArray, JsonRecurPropertyToIcalKeywordArray},
    { "BYMONTHDAY", "bymonthday", BONGO_JSON_ARRAY, IcalRecurPropertyToJsonIntArray, JsonRecurPropertyToIcalIntArray },
    { "BYYEARDAY", "byyearday", BONGO_JSON_ARRAY, IcalRecurPropertyToJsonIntArray, JsonRecurPropertyToIcalIntArray },
    { "BYWEEKNO", "byweekno", BONGO_JSON_ARRAY, IcalRecurPropertyToJsonIntArray, JsonRecurPropertyToIcalIntArray },
    { "BYMONTH", "bymonth", BONGO_JSON_ARRAY, IcalRecurPropertyToJsonIntArray, JsonRecurPropertyToIcalIntArray },
    { "BYSETPOS", "bysetpos", BONGO_JSON_INT, IcalRecurPropertyToJsonInt, JsonRecurPropertyToIcalInt },
    { "WKST", "weekstart", BONGO_JSON_STRING, IcalRecurPropertyToJsonKeyword, JsonRecurPropertyToIcalKeyword },
};

ValueType valueTypes[] = {
    { ICAL_STRING_VALUE, IcalValueToJsonString, JsonValueToIcalString },
    { ICAL_TEXT_VALUE, IcalValueToJsonText, JsonValueToIcalText },
    { ICAL_DATE_VALUE, IcalValueToJsonDate, JsonValueToIcalDate },
    { ICAL_DATE_VALUE, IcalValueToJsonTime, JsonValueToIcalTime },
    { ICAL_GEO_VALUE, IcalValueToJsonGeo, JsonValueToIcalGeo },
    { ICAL_PERIOD_VALUE, IcalValueToJsonPeriod, JsonValueToIcalPeriod },
    { ICAL_FLOAT_VALUE, IcalValueToJsonFloat, JsonValueToIcalFloat },
    { ICAL_DATETIMEPERIOD_VALUE, IcalValueToJsonDateTimePeriod, JsonValueToIcalDateTimePeriod },
    { ICAL_INTEGER_VALUE, IcalValueToJsonInt, JsonValueToIcalInt },
    { ICAL_DURATION_VALUE, IcalValueToJsonDuration, JsonValueToIcalDuration },
    { ICAL_BOOLEAN_VALUE, IcalValueToJsonBool, JsonValueToIcalBool },
    { ICAL_TRIGGER_VALUE, IcalValueToJsonDuration, JsonValueToIcalDuration },
    { ICAL_RECUR_VALUE, IcalValueToJsonRecur, JsonValueToIcalRecur },
    { ICAL_DATETIME_VALUE, IcalValueToJsonDateTime, JsonValueToIcalDateTime },
};
 
PropertyType veventTypes[] = {
    { ICAL_CLASS_PROPERTY, "class", FALSE },
    { ICAL_CREATED_PROPERTY, "created", FALSE },
    { ICAL_DESCRIPTION_PROPERTY, "description", FALSE },
    { ICAL_DTSTART_PROPERTY, "dtstart", FALSE },
    { ICAL_GEO_PROPERTY, "geo", FALSE },
    { ICAL_LASTMODIFIED_PROPERTY, "lastmodified", FALSE },
    { ICAL_LOCATION_PROPERTY, "location", FALSE },
    { ICAL_ORGANIZER_PROPERTY, "organizer", FALSE },
    { ICAL_PRIORITY_PROPERTY, "priority", FALSE },
    { ICAL_DTSTAMP_PROPERTY, "dtstamp", FALSE },
    { ICAL_SEQUENCE_PROPERTY, "sequence", FALSE },
    { ICAL_STATUS_PROPERTY, "status", FALSE },
    { ICAL_SUMMARY_PROPERTY, "summary", FALSE },
    { ICAL_TRANSP_PROPERTY, "transp", FALSE },
    { ICAL_UID_PROPERTY, "uid", FALSE },
    { ICAL_URL_PROPERTY, "url", FALSE },
    { ICAL_RECURRENCEID_PROPERTY, "recurid", FALSE },
    { ICAL_DTEND_PROPERTY, "dtend", FALSE },
    { ICAL_DURATION_PROPERTY, "duration", FALSE },
    { ICAL_ATTACH_PROPERTY, "attachments", TRUE },
    { ICAL_ATTENDEE_PROPERTY, "attendees", TRUE },
    { ICAL_CATEGORIES_PROPERTY, "categories", TRUE },
    { ICAL_COMMENT_PROPERTY, "comment", TRUE },
    { ICAL_CONTACT_PROPERTY, "contact", TRUE },
    { ICAL_EXDATE_PROPERTY, "exdates", TRUE },
    { ICAL_EXRULE_PROPERTY, "exrules", TRUE },
    { ICAL_REQUESTSTATUS_PROPERTY, "requeststatus", TRUE },
    { ICAL_RELATEDTO_PROPERTY, "relatedto", TRUE },
    { ICAL_RESOURCES_PROPERTY, "resources", TRUE },
    { ICAL_RDATE_PROPERTY, "rdates", TRUE },
    { ICAL_RRULE_PROPERTY, "rrules", TRUE },
};
 
PropertyType vcalendarTypes[] = {
    { ICAL_PRODID_PROPERTY, "prodid", FALSE },
    { ICAL_VERSION_PROPERTY, "version", FALSE },
    { ICAL_CALSCALE_PROPERTY, "calscale", FALSE },
    { ICAL_METHOD_PROPERTY, "method", FALSE },
};

PropertyType vtimezoneTypes[] = {
    { ICAL_TZID_PROPERTY, "tzid", FALSE },
    { ICAL_LASTMODIFIED_PROPERTY, "lastmodified", FALSE },
};

PropertyType daylightstandardTypes[] = {
    { ICAL_DTSTART_PROPERTY, "dtstart", FALSE },
    { ICAL_TZOFFSETTO_PROPERTY, "tzoffsetto", FALSE },
    { ICAL_TZOFFSETFROM_PROPERTY, "tzoffsetfrom", FALSE },
    { ICAL_COMMENT_PROPERTY, "comment", TRUE },
    { ICAL_RDATE_PROPERTY, "rdates", TRUE },
    { ICAL_RRULE_PROPERTY, "rrules", TRUE },
    { ICAL_TZNAME_PROPERTY, "tzname", TRUE },
};

PropertyType vtodoTypes[] = {
    { ICAL_CLASS_PROPERTY, "class", FALSE },
    { ICAL_COMPLETED_PROPERTY, "completed", FALSE },
    { ICAL_CREATED_PROPERTY, "created", FALSE },
    { ICAL_DESCRIPTION_PROPERTY, "description", FALSE },
    { ICAL_DTSTART_PROPERTY, "dtstart", FALSE },
    { ICAL_GEO_PROPERTY, "geo", FALSE },
    { ICAL_LASTMODIFIED_PROPERTY, "lastmodified", FALSE },
    { ICAL_LOCATION_PROPERTY, "location", FALSE },
    { ICAL_ORGANIZER_PROPERTY, "organizer", FALSE },
    { ICAL_PERCENTCOMPLETE_PROPERTY, "percentcomplete", FALSE },
    { ICAL_PRIORITY_PROPERTY, "priority", FALSE },
    { ICAL_DTSTAMP_PROPERTY, "dtstamp", FALSE },
    { ICAL_SEQUENCE_PROPERTY, "sequence", FALSE },
    { ICAL_STATUS_PROPERTY, "status", FALSE },
    { ICAL_SUMMARY_PROPERTY, "summary", FALSE },
    { ICAL_TRANSP_PROPERTY, "transp", FALSE },
    { ICAL_UID_PROPERTY, "uid", FALSE },
    { ICAL_URL_PROPERTY, "url", FALSE },
    { ICAL_RECURRENCEID_PROPERTY, "recurid", FALSE },
    { ICAL_DURATION_PROPERTY, "duration", FALSE },
    { ICAL_DUE_PROPERTY, "due", FALSE },
    { ICAL_ATTACH_PROPERTY, "attachments", TRUE },
    { ICAL_ATTENDEE_PROPERTY, "attendees", TRUE },
    { ICAL_CATEGORIES_PROPERTY, "categories", TRUE },
    { ICAL_COMMENT_PROPERTY, "comment", TRUE },
    { ICAL_CONTACT_PROPERTY, "contact", TRUE },
    { ICAL_EXDATE_PROPERTY, "exdates", TRUE },
    { ICAL_EXRULE_PROPERTY, "exrules", TRUE },
    { ICAL_REQUESTSTATUS_PROPERTY, "requeststatus", TRUE },
    { ICAL_RELATEDTO_PROPERTY, "relatedto", TRUE },
    { ICAL_RESOURCES_PROPERTY, "resources", TRUE },
    { ICAL_RDATE_PROPERTY, "rdates", TRUE },
    { ICAL_RRULE_PROPERTY, "rrules", TRUE },
};


PropertyType vjournalTypes[] = {
    { ICAL_CLASS_PROPERTY, "class", FALSE },
    { ICAL_CREATED_PROPERTY, "created", FALSE },
    { ICAL_DESCRIPTION_PROPERTY, "description", FALSE },
    { ICAL_DTSTART_PROPERTY, "dtstart", FALSE },
    { ICAL_LASTMODIFIED_PROPERTY, "lastmodified", FALSE },
    { ICAL_ORGANIZER_PROPERTY, "organizer", FALSE },
    { ICAL_DTSTAMP_PROPERTY, "dtstamp", FALSE },
    { ICAL_SEQUENCE_PROPERTY, "sequence", FALSE },
    { ICAL_STATUS_PROPERTY, "status", FALSE },
    { ICAL_SUMMARY_PROPERTY, "summary", FALSE },
    { ICAL_UID_PROPERTY, "uid", FALSE },
    { ICAL_URL_PROPERTY, "url", FALSE },
    { ICAL_RECURRENCEID_PROPERTY, "recurid", FALSE },
    { ICAL_DURATION_PROPERTY, "duration", FALSE },
    { ICAL_DUE_PROPERTY, "due", FALSE },
    { ICAL_ATTACH_PROPERTY, "attachments", TRUE },
    { ICAL_ATTENDEE_PROPERTY, "attendees", TRUE },
    { ICAL_CATEGORIES_PROPERTY, "categories", TRUE },
    { ICAL_COMMENT_PROPERTY, "comment", TRUE },
    { ICAL_CONTACT_PROPERTY, "contact", TRUE },
    { ICAL_EXDATE_PROPERTY, "exdates", TRUE },
    { ICAL_EXRULE_PROPERTY, "exrules", TRUE },
    { ICAL_REQUESTSTATUS_PROPERTY, "requeststatus", TRUE },
    { ICAL_RELATEDTO_PROPERTY, "relatedto", TRUE },
    { ICAL_RDATE_PROPERTY, "rdates", TRUE },
    { ICAL_RRULE_PROPERTY, "rrules", TRUE },
};

PropertyType vfreebusyTypes[] = {
    { ICAL_CONTACT_PROPERTY, "contact", FALSE },
    { ICAL_DTSTART_PROPERTY, "dtstart", FALSE },
    { ICAL_DTEND_PROPERTY, "dtend", FALSE },
    { ICAL_DTSTAMP_PROPERTY, "dtstamp", FALSE },
    { ICAL_DURATION_PROPERTY, "duration", FALSE },
    { ICAL_ORGANIZER_PROPERTY, "organizer", FALSE },
    { ICAL_UID_PROPERTY, "uid", FALSE },
    { ICAL_URL_PROPERTY, "url", FALSE },
    { ICAL_ATTENDEE_PROPERTY, "attendees", TRUE },
    { ICAL_COMMENT_PROPERTY, "comment", TRUE },
    { ICAL_FREEBUSY_PROPERTY, "freebusy", TRUE },
    { ICAL_REQUESTSTATUS_PROPERTY, "requeststatus", TRUE },
};

PropertyType valarmTypes[] = {
    { ICAL_ACTION_PROPERTY, "action", FALSE },
    { ICAL_DESCRIPTION_PROPERTY, "description", FALSE },
    { ICAL_SUMMARY_PROPERTY, "description", FALSE },
    { ICAL_TRIGGER_PROPERTY, "trigger", FALSE },
    { ICAL_DURATION_PROPERTY, "duration", FALSE },
    { ICAL_REPEAT_PROPERTY, "repeat", FALSE },
    { ICAL_ATTACH_PROPERTY, "attachments", TRUE },
};

ComponentType componentTypes[] = {
    { BONGO_CAL_TYPE_CALENDAR, ICAL_VCALENDAR_COMPONENT, NULL, vcalendarTypes, sizeof(vcalendarTypes) / sizeof(PropertyType) },
    { BONGO_CAL_TYPE_EVENT, ICAL_VEVENT_COMPONENT, "event", veventTypes, sizeof(veventTypes) / sizeof(PropertyType) },
    { BONGO_CAL_TYPE_TODO, ICAL_VTODO_COMPONENT, "todo", vtodoTypes, sizeof(vtodoTypes) / sizeof(PropertyType) },
    { BONGO_CAL_TYPE_JOURNAL, ICAL_VJOURNAL_COMPONENT, "journal", vjournalTypes, sizeof(vjournalTypes) / sizeof(PropertyType) },
    { BONGO_CAL_TYPE_FREEBUSY, ICAL_VFREEBUSY_COMPONENT, "freebusy", vfreebusyTypes, sizeof(vfreebusyTypes) / sizeof(PropertyType) },
    { BONGO_CAL_TYPE_ALARM, ICAL_VALARM_COMPONENT, "valarm", valarmTypes, sizeof(valarmTypes) / sizeof(PropertyType) },
    { BONGO_CAL_TYPE_AUDIOALARM, ICAL_XAUDIOALARM_COMPONENT, "audioalarm", valarmTypes, sizeof(valarmTypes) / sizeof(PropertyType) },
    { BONGO_CAL_TYPE_DISPLAYALARM, ICAL_XDISPLAYALARM_COMPONENT, "displayalarm", valarmTypes, sizeof(valarmTypes) / sizeof(PropertyType) },
    { BONGO_CAL_TYPE_EMAILALARM, ICAL_XEMAILALARM_COMPONENT, "emailalarm", valarmTypes, sizeof(valarmTypes) / sizeof(PropertyType) },
    { BONGO_CAL_TYPE_PROCEDUREALARM, ICAL_XPROCEDUREALARM_COMPONENT, "procedurealarm", valarmTypes, sizeof(valarmTypes) / sizeof(PropertyType) },
    { BONGO_CAL_TYPE_TIMEZONE, ICAL_VTIMEZONE_COMPONENT, "timezone", vtimezoneTypes, sizeof(vtimezoneTypes) / sizeof(PropertyType) },
    { BONGO_CAL_TYPE_TZSTANDARD, ICAL_XSTANDARD_COMPONENT, "standard", daylightstandardTypes, sizeof(daylightstandardTypes) / sizeof(PropertyType) },
    { BONGO_CAL_TYPE_TZDAYLIGHT, ICAL_XDAYLIGHT_COMPONENT, "daylight", daylightstandardTypes, sizeof(daylightstandardTypes) / sizeof(PropertyType) },

    /* Other components will be converted generically */ 
};

/* Parameter converters */

static void 
IcalParametersToJson(icalproperty *prop, BongoJsonObject *jsonProp)
{
    BongoJsonObject *jsonParams = NULL;
    icalparameter *param;

    param = icalproperty_get_first_parameter(prop, ICAL_ANY_PARAMETER);
    if (param) {
        while (param) {
            int n;
            char *args[2];
            char *str;
            icalparameter_kind kind;

            str = MemStrdup(icalparameter_as_ical_string(param));

            n = BongoStringSplit(str, '=', args, 2);
            if (n == 1) {
                args[1] = "";
            }

            kind = icalparameter_isa(param);
            
            if (!jsonParams) {
                jsonParams = BongoJsonObjectNew();
            }

            if (kind == ICAL_VALUE_PARAMETER) {
                BongoJsonObjectPutString(jsonParams, "type", args[1]);
            } else if (kind == ICAL_RSVP_PARAMETER) {
                BongoJsonObjectPutBool(jsonParams, "rsvp", (XplStrCaseCmp(args[1], "TRUE") == 0));
            } else {
                BongoStrToLower(args[0]);
                BongoJsonObjectPutString(jsonParams, args[0], args[1]);
            }
            MemFree(str);
            param = icalproperty_get_next_parameter(prop, ICAL_ANY_PARAMETER);
        }
    }

    if (jsonParams) {
        BongoJsonObjectPutObject(jsonProp, "params", jsonParams);
    }
}

static void 
JsonParametersToIcal(BongoJsonObject *obj, icalproperty *prop)
{
    BongoJsonObjectIter iter;
    BongoJsonObject *child;
    
    if (BongoJsonObjectGetObject(obj, "params", &child) != BONGO_JSON_OK) {
        return;
    }

    for (BongoJsonObjectIterFirst(obj, &iter);
         iter.key != NULL;
         BongoJsonObjectIterNext(obj, &iter)) {
        icalparameter *param;
        
        param = NULL;
        if (XplStrCaseCmp(iter.key, "rsvp")) {
            if (iter.value->type == BONGO_JSON_BOOL) {
                param = icalparameter_new_from_value_string(ICAL_RSVP_PARAMETER, 
                                                            BongoJsonNodeAsBool(iter.value) ? "TRUE" : "FALSE");
            }                                      
        } else if (XplStrCaseCmp(iter.key, "type")) {
            if (iter.value->type == BONGO_JSON_STRING) {
                param = icalparameter_new_from_value_string(ICAL_VALUE_PARAMETER,
                                                            BongoJsonNodeAsString(iter.value));
            }
        } else {
            if (iter.value->type == BONGO_JSON_STRING) {
                char buffer[1024];
                char *key;
                
                key = MemStrdup(iter.key);
                BongoStrToUpper(key);

                snprintf(buffer, 1024, "%s=%s", key, BongoJsonNodeAsString(iter.value));
                param = icalparameter_new_from_string(buffer);
            }
        }

        if (param) {
            icalproperty_add_parameter(prop, param);
        }
    }
}

/* Value converters */

static void
IcalValueToJsonString(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind)
{
    BongoJsonObjectPutString(obj, "value", icalproperty_get_value_as_string(prop));
}

static void
JsonValueToIcalString(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind)
{
    const char *val;

    if (BongoJsonObjectGetString(obj, "value", &val) == BONGO_JSON_OK) {
        icalproperty_set_value_from_string(prop, val, icalvalue_kind_to_string(kind));
    }
}

static void
IcalValueToJsonText(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind)
{
    icalvalue *value;

    value = icalproperty_get_value(prop);
    BongoJsonObjectPutString(obj, "value", icalvalue_get_text(value));
}

static void
JsonValueToIcalText(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind)
{
    const char *val;

    if (BongoJsonObjectGetString(obj, "value", &val) == BONGO_JSON_OK) {
        icalvalue *value;
    
        value = icalvalue_new_text(val);
        icalproperty_set_value(prop, value);
    }
}

static void
IcalValueToJsonBool(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind)
{
    BongoJsonObjectPutBool(obj, "value", icalvalue_get_boolean(icalproperty_get_value(prop)));
}

static void
JsonValueToIcalBool(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind)
{
    BOOL val;

    if (BongoJsonObjectGetBool(obj, "value", &val) == BONGO_JSON_OK) {
        icalproperty_set_value(prop, icalvalue_new_boolean(val));
    }
}

static void
IcalValueToJsonDate(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind)
{
    /* FIXME: treating dates as strings? */
    BongoJsonObjectPutString(obj, "value", icalproperty_get_value_as_string(prop));
}

static void
JsonValueToIcalDate(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind)
{
    const char *val;

    /* FIXME: treating dates as strings? */

    if (BongoJsonObjectGetString(obj, "value", &val) == BONGO_JSON_OK) {
        icalproperty_set_value_from_string(prop, val, "DATE");
    }
}

static void
IcalValueToJsonDateTime(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind)
{
    /* FIXME: treating datetimes as strings? */
    BongoJsonObjectPutString(obj, "value", icalproperty_get_value_as_string(prop));
}

static void
JsonValueToIcalDateTime(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind)
{
    const char *val;

    /* FIXME: treating dates as strings? */

    if (BongoJsonObjectGetString(obj, "value", &val) == BONGO_JSON_OK) {
        icalproperty_set_value_from_string(prop, val, "DATE-TIME");
    }
}

static void
IcalValueToJsonDuration(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind)
{
    /* FIXME: treating dates as strings? */
    BongoJsonObjectPutString(obj, "value", icalproperty_get_value_as_string(prop));
}

static void
JsonValueToIcalDuration(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind)
{
    const char *val;

    /* FIXME: treating dates as strings? */

    if (BongoJsonObjectGetString(obj, "value", &val) == BONGO_JSON_OK) {
        icalproperty_set_value_from_string(prop, val, "DURATION");
    }
}

static void
IcalValueToJsonFloat(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind)
{
    BongoJsonObjectPutDouble(obj, "value", icalvalue_get_float(icalproperty_get_value(prop)));
}

static void
JsonValueToIcalFloat(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind)
{
    double val;

    /* FIXME: treating dates as strings? */

    if (BongoJsonObjectGetDouble(obj, "value", &val) == BONGO_JSON_OK) {
        icalproperty_set_value(prop, icalvalue_new_float(val));
    }
}

static void
IcalValueToJsonInt(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind)
{
    BongoJsonObjectPutInt(obj, "value", icalvalue_get_integer(icalproperty_get_value(prop)));
}

static void
JsonValueToIcalInt(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind)
{
    int val;

    /* FIXME: treating dates as strings? */

    if (BongoJsonObjectGetInt(obj, "value", &val) == BONGO_JSON_OK) {
        icalproperty_set_value(prop, icalvalue_new_integer(val));
    }
}

static void
IcalValueToJsonPeriod(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind)
{
    struct icalperiodtype period;
    BongoJsonObject *value;

    value = BongoJsonObjectNew();

    period = icalvalue_get_period(icalproperty_get_value(prop));

    BongoJsonObjectPutString(value, "start", icaltime_as_ical_string(period.start));
    
    if (!icaltime_is_null_time(period.end)) {
        BongoJsonObjectPutString(value, "end", icaltime_as_ical_string(period.end));
    } else {
        BongoJsonObjectPutString(value, "duration", icaldurationtype_as_ical_string(period.duration));
    }

    BongoJsonObjectPutObject(obj, "value", value);
}

static void
JsonValueToIcalPeriod(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind)
{
    const char *val;
    struct icalperiodtype period;
    BongoJsonObject *jsonValue;

    if (BongoJsonObjectGetObject(obj, "value", &jsonValue) != BONGO_JSON_OK) {
        return;
    }
    
    if (BongoJsonObjectGetString(jsonValue, "start", &val) == BONGO_JSON_OK) {
        period.start = icaltime_from_string(val);
    }

    if (BongoJsonObjectGetString(jsonValue, "end", &val) == BONGO_JSON_OK) {
        period.end = icaltime_from_string(val);
    } else {
        period.end = icaltime_null_time();
    }

    if (BongoJsonObjectGetString(jsonValue, "duration", &val) == BONGO_JSON_OK) {
        period.duration = icaldurationtype_from_string(val);
    }

    icalproperty_set_value(prop, icalvalue_new_period(period));
}

static void
IcalValueToJsonDateTimePeriod(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind)
{
    struct icaldatetimeperiodtype dtperiod;
    BongoJsonObject *value;

    value = BongoJsonObjectNew();

    dtperiod = icalvalue_get_datetimeperiod(icalproperty_get_value(prop));

    if (!icaltime_is_null_time(dtperiod.time)) {
        return IcalValueToJsonDateTime(prop, obj, kind);
    } else {
        return IcalValueToJsonPeriod(prop, obj, kind);
    }
}

static void
JsonValueToIcalDateTimePeriod(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind)
{
    BongoJsonNode *valueNode;
    
    if (BongoJsonObjectGet(obj, "value", &valueNode) != BONGO_JSON_OK) {
        return;
    }
    
    if (valueNode->type == BONGO_JSON_STRING) {
        return JsonValueToIcalDateTime(obj, prop, kind);
    } else if (valueNode->type == BONGO_JSON_OBJECT) {
        return JsonValueToIcalPeriod(obj, prop, kind);
    }
}

static void
IcalRecurPropertyToJsonKeyword(char *key, char *value, BongoJsonObject *obj)
{
    BongoStrToLower(value);
    BongoJsonObjectPutString(obj, key, value);
}

static void
JsonRecurPropertyToIcalKeyword(char *key, BongoJsonNode *value, BongoStringBuilder *sb)
{
    char *dup;
    dup = BongoMemStrToUpper(BongoJsonNodeAsString(value));
    BongoStringBuilderAppendF(sb, "%s=%s", key, dup);
    MemFree(dup);
}

static void
IcalRecurPropertyToJsonString(char *key, char *value, BongoJsonObject *obj)
{
    BongoJsonObjectPutString(obj, key, value);
}

static void
JsonRecurPropertyToIcalString(char *key, BongoJsonNode *value, BongoStringBuilder *sb)
{
    BongoStringBuilderAppendF(sb, "%s=%s", key, BongoJsonNodeAsString(value));
}

static void
IcalRecurPropertyToJsonInt(char *key, char *value, BongoJsonObject *obj)
{
    BongoJsonObjectPutInt(obj, key, atoi(value));
}

static void
JsonRecurPropertyToIcalInt(char *key, BongoJsonNode *value, BongoStringBuilder *sb)
{
    BongoStringBuilderAppendF(sb, "%s=%d", key, BongoJsonNodeAsInt(value));
}

static void
IcalRecurPropertyToJsonKeywordArray(char *key, char *value, BongoJsonObject *obj)
{
    BongoArray *array;
    char *dup;
    char *split[100];
    int n;
    int i;

    dup = MemStrdup(value);    
    array = BongoArrayNew(sizeof(BongoJsonNode*), 10);

    do {
        n = BongoStringSplit(value, ',', split, 100);
        for(i = 0; i < n && i < 99; i++) {
            BongoStrToLower(split[i]);
            BongoJsonArrayAppendString(array, split[i]);
        }

        value = split[99];
    } while (n == 100); 

    BongoJsonObjectPutArray(obj, key, array);
}

static void
JsonRecurPropertyToIcalKeywordArray(char *key, BongoJsonNode *value, BongoStringBuilder *sb)
{
    BongoArray *array;
    char *dup;
    BOOL first = TRUE;
    
    array = BongoJsonNodeAsArray(value);

    if (array && array->len > 0) {
        unsigned int i;

        for (i = 0; i < array->len; i++) {
            const char *val;
            if (BongoJsonArrayGetString(array, i, &val) == BONGO_JSON_OK) {
                dup = BongoMemStrToUpper(val);
                if (first) {
                    BongoStringBuilderAppendF(sb, "%s=", key);
                } else {
                    BongoStringBuilderAppend(sb, ",");
                }
                BongoStringBuilderAppendF(sb, "%s", dup);
                MemFree(dup);
                first = FALSE;
            }
        }
    }
}

static void
IcalRecurPropertyToJsonIntArray(char *key, char *value, BongoJsonObject *obj)
{
    BongoArray *array;
    char *dup;
    char *split[100];
    int n;
    int i;

    dup = MemStrdup(value);    
    array = BongoArrayNew(sizeof(BongoJsonNode*), 10);

    do {
        n = BongoStringSplit(value, ',', split, 100);
        for(i = 0; i < n && i < 99; i++) {
            BongoJsonArrayAppendInt(array, atoi(split[i]));
        }

        value = split[99];
    } while (n == 100);

    BongoJsonObjectPutArray(obj, key, array);
}

static void
JsonRecurPropertyToIcalIntArray(char *key, BongoJsonNode *value, BongoStringBuilder *sb)
{
    BongoArray *array;
    BOOL first = TRUE;
    
    array = BongoJsonNodeAsArray(value);

    if (array && array->len > 0) {
        unsigned int i;

        for (i = 0; i < array->len; i++) {
            int val;
            if (BongoJsonArrayGetInt(array, i, &val) == BONGO_JSON_OK) {
                if (first) {
                    BongoStringBuilderAppendF(sb, "%s=", key);
                } else {
                    BongoStringBuilderAppend(sb, ",");
                }
                BongoStringBuilderAppendF(sb, "%d", val);
                first = FALSE;
            }
        }
    }
}

static RecurProperty *
FindRecurPropertyForIcalString(const char *key)
{
    unsigned int i;
    
    for (i = 0; i < sizeof(recurProperties) / sizeof(RecurProperty); i++) {
        RecurProperty *p;
        p = &recurProperties[i];
        if (!XplStrCaseCmp(p->icalKey, key)) {
            return p;
        } 
    }

    return NULL;
}

static RecurProperty *
FindRecurPropertyForJsonString(const char *key)
{
    unsigned int i;
    
    for (i = 0; i < sizeof(recurProperties) / sizeof(RecurProperty); i++) {
        RecurProperty *p;
        p = &recurProperties[i];
        if (!XplStrCaseCmp(p->jsonKey, key)) {
            return p;
        } 
    }

    return NULL;
}

static void
IcalRecurArgToJson(BongoJsonObject *obj, char *key, char *value)
{
    RecurProperty *prop;
    
    prop = FindRecurPropertyForIcalString(key);

    if (prop) {
        prop->toJson(prop->jsonKey, value, obj);
    } else {
#warning X- bits not implemented
    }
}
static void
IcalValueToJsonRecur(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind)
{
    /* FIXME: treating recurs as strings? */
    char *propval;
    char *p;
    char *p2;
    BongoJsonObject *jsonValue;
    
    jsonValue = BongoJsonObjectNew();
    
    propval = MemStrdup(icalproperty_get_value_as_string(prop));

    /* FIXME */
    p = propval;
    while (p && *p) {
        char *val;
        p2 = strchr(p, ';');
        if (p2) {
            *p2 = '\0';
        }
        
        val = strchr(p, '=');
        
        if (val) {
            *val++ = '\0';
        } else {
            val = "";
        }

        DPRINT("converting recur: %s = %s\n", p, val);

        IcalRecurArgToJson(jsonValue, p, val);

        if (p2) {
            p = p2 + 1;
        } else {
            p = 0;
        }
    }

    MemFree(propval);

    BongoJsonObjectPutObject(obj, "value", jsonValue);
}

static void
JsonValueToIcalRecur(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind)
{
    BongoJsonObjectIter iter;
    BongoStringBuilder sb;
    BOOL first = TRUE;
    BongoJsonObject *jsonValue;
    
    if (BongoJsonObjectGetObject(obj, "value", &jsonValue) != BONGO_JSON_OK) {
        return;
    }
    
    BongoStringBuilderInit(&sb);

    for (BongoJsonObjectIterFirst(jsonValue, &iter);
         iter.key != NULL;
         BongoJsonObjectIterNext(jsonValue, &iter)) {
        RecurProperty *recurProp;
        
        recurProp = FindRecurPropertyForJsonString(iter.key);
        
        if (recurProp) {
            if (iter.value->type == recurProp->jsonType) {
                if (!first) {
                    BongoStringBuilderAppendChar(&sb, ';');
                }
                recurProp->toIcal(recurProp->icalKey, iter.value, &sb);
                first = FALSE;
            }
        } else {
#warning X- bits not implemented
        }
    }

    icalproperty_set_value_from_string(prop, sb.value, "RECUR");

    MemFree(sb.value);
}

static void
IcalValueToJsonTime(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind)
{
    /* FIXME: treating times as strings? */

    BongoJsonObjectPutString(obj, "value", icalproperty_get_value_as_string(prop));
}

static void
JsonValueToIcalTime(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind)
{
    const char *val;

    /* FIXME: treating times as strings? */
    if (BongoJsonObjectGetString(obj, "value", &val) == BONGO_JSON_OK) {
        icalproperty_set_value_from_string(prop, val, "TIME");
    }
}


static void
IcalValueToJsonGeo(icalproperty *prop, BongoJsonObject *obj, icalvalue_kind kind)
{
    /* FIXME: treating geos as strings? */
    BongoJsonObjectPutString(obj, "value", icalproperty_get_value_as_string(prop));
}

static void
JsonValueToIcalGeo(BongoJsonObject *obj, icalproperty *prop, icalvalue_kind kind)
{
    const char *val;

    /* FIXME: treating geos as strings? */
    if (BongoJsonObjectGetString(obj, "value", &val) == BONGO_JSON_OK) {
        icalproperty_set_value_from_string(prop, val, "GEO");
    }
}

static ValueType *
GetValueType(icalvalue_kind kind) 
{
    unsigned int i;
    
    for (i = 0; i < sizeof(valueTypes) / sizeof (ValueType); i++) {
        if (valueTypes[i].icalKind == kind) {
            return &valueTypes[i];
        }
    }
    
    /* Treat unknown types as strings */
    return &valueTypes[0];
}

/* Property conversion */

BongoJsonObject *
BongoIcalPropertyToJson(icalproperty *prop)
{
    BongoJsonObject *ret;
    icalvalue *value;
    icalvalue_kind valueKind;
    ValueType *valueType;

    DPRINT("converting prop %s", icalproperty_as_ical_string(prop));

    value = icalproperty_get_value(prop);
    valueKind = icalvalue_isa(value);
    valueType = GetValueType(valueKind);

    ret = BongoJsonObjectNew();

    IcalParametersToJson(prop, ret);
    valueType->toJson(prop, ret, valueKind);

    DPRINT("got prop %s\n", BongoJsonObjectToString(ret));

    return ret;
}

static icalproperty *
JsonPropertyToIcal(const char *propertyName, icalproperty_kind propertyKind, BongoJsonObject *obj)
{
    icalproperty *ret;
    icalvalue_kind valueKind;
    ValueType *valueType;
    const char *type;

    if (BongoJsonObjectGetString(obj, "type", &type) == BONGO_JSON_OK) {
        valueKind = icalvalue_string_to_kind(type);
    } else {
        valueKind = icalproperty_kind_to_value_kind(propertyKind);
    }

    valueType = GetValueType(valueKind);

    ret = icalproperty_new(propertyKind);

    if (propertyKind == ICAL_X_PROPERTY) {
        if ((propertyName[0] == 'X' || propertyName[0] == 'x') && propertyName[1] == '-') {
            icalproperty_set_x_name(ret, propertyName);
        } else {
            char fullName[1024];
            snprintf(fullName, sizeof(fullName), "X-BONGO-%s", propertyName);
            icalproperty_set_x_name(ret, fullName);
        }
    }

    JsonParametersToIcal(obj, ret);
    valueType->toIcal(obj, ret, valueKind);

    return ret;
}

static PropertyType *
FindPropertyTypeForIcalKind(PropertyType *types, int n, icalproperty_kind kind)
{
    int i;
    for (i = 0; i < n; i++) {
        if (types[i].icalKind == kind) {
            return &types[i];
        }
    }
    return NULL;
}

static PropertyType *
FindPropertyTypeForJsonName(PropertyType *types, int n, const char *name)
{
    /* intern strings? */
    int i;
    for (i = 0; i < n; i++) {
        if (!XplStrCaseCmp(types[i].jsonName, name)) {
            return &types[i];
        }
    }
    return NULL;    
}

static ComponentType *
FindComponentTypeForIcal(icalcomponent *comp)
{
    icalcomponent_kind kind;
    unsigned int i;

    kind = icalcomponent_isa(comp);
    for (i = 0; i < sizeof(componentTypes) / sizeof(ComponentType); i++) {
        if (componentTypes[i].icalKind == kind) {
            return &componentTypes[i];
        }
    }
    
    return NULL;
}

static ComponentType *
FindComponentTypeForJson(BongoJsonObject *obj)
{
    BongoArray *children;
    
    /* A toplevel/vcalendar json component will have a "components" child */
    if (BongoJsonObjectGetArray(obj, "components", &children) == BONGO_JSON_OK) {
        return &componentTypes[0];
    } else {
        const char *type;
        unsigned int i;

        if (BongoJsonObjectGetString(obj, "type", &type) == BONGO_JSON_OK) {
            for (i = 0; i < sizeof(componentTypes) / sizeof(ComponentType); i++) {
                if (componentTypes[i].jsonName && !XplStrCaseCmp(componentTypes[i].jsonName, type)) {
                    return &componentTypes[i];
                }
            }
        }
        
        return NULL;    
    }    
}

/* Component conversion */

static void
AddIcalSinglePropertyToJson(BongoJsonObject *obj, const char *name, icalproperty *prop)
{
    BongoJsonObject *jsonProp;
    
    jsonProp = BongoIcalPropertyToJson(prop);
    
    BongoJsonObjectPutObject(obj, name, jsonProp);
}

static void
AddIcalMultiPropertyToJson(BongoJsonObject *obj, const char *name, icalproperty *prop)
{
    BongoArray *array;
    BongoJsonObject *jsonProp;

    assert(name != NULL);

    if (BongoJsonObjectGetArray(obj, name, &array) != BONGO_JSON_OK) {
        array = BongoArrayNew(sizeof(BongoJsonNode*), 10);
        BongoJsonObjectPutArray(obj, name, array);
    }
    
    jsonProp = BongoIcalPropertyToJson(prop);
    BongoJsonArrayAppendObject(array, jsonProp);
}

static BongoJsonObject *
IcalComponentToJsonWithType(ComponentType *componentType, icalcomponent *component, BOOL recurse) 
{
    icalcomponent *child;
    icalproperty *prop;
    BongoJsonObject *obj;
    
    obj = BongoJsonObjectNew();
    if (componentType->jsonName) {
        BongoJsonObjectPutString(obj, "type", componentType->jsonName);
    }

    for (prop = icalcomponent_get_first_property (component, ICAL_ANY_PROPERTY);
         prop;
         prop = icalcomponent_get_next_property (component, ICAL_ANY_PROPERTY)) {
        PropertyType *type;

        type = FindPropertyTypeForIcalKind(componentType->propTypes, 
                                           componentType->numTypes, 
                                           icalproperty_isa(prop));
        if (type && type->multiValue) {
            AddIcalMultiPropertyToJson(obj, type->jsonName, prop);
        } else if (type) {
            AddIcalSinglePropertyToJson(obj, type->jsonName, prop);
        } else {
            AddIcalMultiPropertyToJson(obj, icalproperty_get_property_name(prop), prop);
        }
    }

    if (recurse) {
        for (child = icalcomponent_get_first_component(component, ICAL_ANY_COMPONENT);
             child;
             child  = icalcomponent_get_next_component(component, ICAL_ANY_COMPONENT)) {
            BongoJsonObject *childObj;
            BongoArray *children;
            char *childAttribute;
            icalcomponent_kind childType;
            
            childObj = BongoIcalComponentToJson(child, TRUE);
            
            childAttribute = "children";
            childType = icalcomponent_isa(child);
            
            switch(icalcomponent_isa(component)) {
            case ICAL_VTIMEZONE_COMPONENT :
                if (childType == ICAL_XSTANDARD_COMPONENT || 
                    childType == ICAL_XDAYLIGHT_COMPONENT) {
                    childAttribute = "changes";
                }
                break;
            case ICAL_VEVENT_COMPONENT :
            case ICAL_VTODO_COMPONENT :
                if (childType == ICAL_VALARM_COMPONENT) {
                    childAttribute = "alarms";
                }
                break;
            default :
                break;
            }
            
            if (BongoJsonObjectGetArray(obj, childAttribute, &children) != BONGO_JSON_OK) {
                children = BongoArrayNew(sizeof(BongoJsonNode*), 2);
                BongoJsonObjectPutArray(obj, childAttribute, children);
            }
            
            BongoJsonArrayAppendObject(children, childObj);
        }
    }

    return obj;
}

BongoJsonObject *
BongoIcalComponentToJson(icalcomponent *component, BOOL recurse) 
{
    ComponentType *typep;
    ComponentType dummyType;
    
    typep = FindComponentTypeForIcal(component);
    if (!typep) {
        dummyType.icalKind = icalcomponent_isa(component);
        dummyType.jsonName = icalcomponent_kind_to_string(dummyType.icalKind);
        dummyType.propTypes = NULL;
        dummyType.numTypes = 0;
        typep = &dummyType;
    }

    return IcalComponentToJsonWithType(typep, component, recurse);
}

static void
AddJsonValueToIcal(icalcomponent *comp, const char *propertyName, PropertyType *type, BongoJsonObject *obj)
{
    icalproperty *prop;
    icalproperty_kind propertyKind;
    
    if (type) {
        propertyKind =  type->icalKind;
    } else {
        propertyKind = ICAL_X_PROPERTY;
    }
    
    prop = JsonPropertyToIcal(propertyName, propertyKind, obj);
    if (prop) {
        icalcomponent_add_property(comp, prop);
    }
}

static void
JsonComponentArrayToIcal(BongoArray *array, icalcomponent *parent, BOOL recurse)
{
    unsigned int i;
    
    for (i = 0; i < array->len; i++) {
        BongoJsonObject *child;
        
        if (BongoJsonArrayGetObject(array, i, &child) == BONGO_JSON_OK) {
            icalcomponent *comp;

            comp = BongoJsonComponentToIcal(child, parent, recurse);
            if (comp) {
                icalcomponent_add_component(parent, comp);
            }
        }
    }
}

static icalcomponent *
JsonComponentToIcalWithType(ComponentType *componentType, BongoJsonObject *obj, icalcomponent *parent, BOOL recurse)
{
    icalcomponent *comp;
    BongoJsonObjectIter iter;
    
    comp = icalcomponent_new(componentType->icalKind);
    
    for (BongoJsonObjectIterFirst(obj, &iter);
         iter.key != NULL;
         BongoJsonObjectIterNext(obj, &iter)) {
        PropertyType *type;
        BongoJsonNode *node;

        /* Ignore structural properties */
        if (!XplStrCaseCmp(iter.key, "type")) {
            continue;
        }
        
        node = iter.value;
        
        type = FindPropertyTypeForJsonName(componentType->propTypes, 
                                           componentType->numTypes,
                                           iter.key);

        if (node->type == BONGO_JSON_ARRAY) {
            BongoArray *array = BongoJsonNodeAsArray(node);
            if (!XplStrCaseCmp(iter.key, "alarms")
                || !XplStrCaseCmp(iter.key, "changes") 
                || !XplStrCaseCmp(iter.key, "components") 
                || !XplStrCaseCmp(iter.key, "children")) {
                if (recurse) {
                    JsonComponentArrayToIcal(array, comp, TRUE);
                }
            } else {
                unsigned int i;
                
                for (i = 0; i < array->len; i++) {
                    BongoJsonObject *child;
                        
                    if (BongoJsonArrayGetObject(array, i, &child) == BONGO_JSON_OK) {
                        AddJsonValueToIcal(comp, iter.key, type, child);
                    }
                }
            }
        } else if (node->type == BONGO_JSON_OBJECT) {
            AddJsonValueToIcal(comp, iter.key, type, BongoJsonNodeAsObject(iter.value));
        }
    }

    return comp;
}

icalcomponent *
BongoJsonComponentToIcal(BongoJsonObject *obj, icalcomponent *parent, BOOL recurse) 
{
    ComponentType *ptype;
    ComponentType dummyType;

    ptype = FindComponentTypeForJson(obj);
    if (!ptype) {
        dummyType.icalKind = ICAL_X_COMPONENT;
        dummyType.jsonName = "";
        dummyType.propTypes = NULL;
        dummyType.numTypes = 0;
        ptype = &dummyType;
    }
    
    return JsonComponentToIcalWithType(ptype, obj, parent, recurse);
}

BongoCalType
BongoCalTypeFromJson(BongoJsonObject *object)
{
    ComponentType *type;
    
    type = FindComponentTypeForJson(object);
    
    if (type) {
        return type->calType;
    } else {
        return BONGO_CAL_TYPE_UNKNOWN;
    }
}

icalcomponent *
BongoCalJsonToIcal(BongoJsonObject *obj)
{
    icalcomponent *comp;

    comp = BongoJsonComponentToIcal(obj, NULL, TRUE);

    return comp;
}

BongoJsonObject *
BongoCalIcalToJson(icalcomponent *comp)
{
    BongoJsonObject *calObj;
    icalcomponent *child;
    BongoArray *components;
    
    DPRINT("converting component: %s\n", icalcomponent_as_ical_string(comp));

    calObj = BongoIcalComponentToJson(comp, FALSE);
    
    components = BongoArrayNew(sizeof(BongoJsonNode *), 10);

    for (child = icalcomponent_get_first_component (comp, ICAL_ANY_COMPONENT);
         child;
         child = icalcomponent_get_next_component (comp, ICAL_ANY_COMPONENT)) {
        BongoJsonObject *childObj;
        
        childObj = BongoIcalComponentToJson(child, TRUE);
        BongoJsonArrayAppendObject(components, childObj);
    }

    BongoJsonObjectPutArray(calObj, "components", components);
    
    return calObj;
}

static BongoHashtable *
GetTimezones(BongoArray *components)
{
    BongoHashtable *timezones;
    unsigned int i;

    timezones = BongoHashtableCreateFull(16, 
                                        (HashFunction)BongoStringHash, (CompareFunction)strcmp, 
                                        NULL, (DeleteFunction)BongoJsonNodeFree);
    
    for (i = 0; i < components->len; i++) {
        BongoJsonNode *objectNode;
        BongoJsonObject *object;
        const char *type;
        
        objectNode = BongoJsonArrayGet(components, i);
        
        object = BongoJsonNodeAsObject(objectNode);
        
        if (objectNode->type != BONGO_JSON_OBJECT) {
            continue;
        }
        
        if (BongoJsonObjectGetString(object, "type", &type) != BONGO_JSON_OK) {
            continue;
        }
        
        if (!XplStrCaseCmp(type, "timezone")) {
            const char *tzid;
            
            if (BongoJsonObjectResolveString(object, "tzid/value", &tzid) == BONGO_JSON_OK) {
                BongoHashtablePut(timezones, (char*)tzid, objectNode);
                BongoArrayIndex(components, BongoJsonNode*, i) = NULL;
            }
        } 
    }

    return timezones;
}

static BOOL
HasTimezone(BongoArray *components, const char *tzid)
{
    unsigned int i;
    for (i = 0; i < components->len; i++) {
        BongoJsonObject *comp;
        if (BongoJsonArrayGetObject(components, i, &comp) == BONGO_JSON_OK) {
            const char *compTzid;
            if (BongoJsonObjectResolveString(comp, "tzid/value", &compTzid) == BONGO_JSON_OK) {
                if (!strcmp(compTzid, tzid)) {
                    return TRUE;
                }
            }
        }
    }
    return FALSE;
}


static void
AddTimezones(BongoArray *newComponents, 
             BongoHashtable *tzs, 
             BongoJsonObject *component)
{
    BongoJsonObjectIter iter;
    
    for (BongoJsonObjectIterFirst(component, &iter);
         iter.key != NULL;
         BongoJsonObjectIterNext(component, &iter)) {
        const char *tzid;
        
        if (BongoJsonNodeResolveString(iter.value, "params/tzid", &tzid) == BONGO_JSON_OK) {
            BongoJsonNode *tzNode;

            if (HasTimezone (newComponents, tzid)) {
                continue;
            }

            tzNode = BongoHashtableGet(tzs, tzid);
            
            if (tzNode) {
                BongoJsonNode *tz;
                
                tz = BongoJsonNodeDup(tzNode);
                BongoJsonArrayAppend(newComponents, tz);
            }
        }   
    }
}

BongoArray *
BongoCalSplit(BongoJsonObject *obj)
{
    BongoJsonNode *componentsNode;
    BongoArray *components;
    unsigned i;
    BongoArray *ret;
    BongoHashtable *tzs;
    BongoHashtable *newObjs;
    
    /* Pull the components out of the toplevel object */
    if (BongoJsonObjectGet(obj, "components", &componentsNode) != BONGO_JSON_OK
        || componentsNode->type != BONGO_JSON_ARRAY) {
        return NULL;
    }
    BongoJsonObjectRemoveSteal(obj, "components");

    /* Don't need the node anymore, grab the array out of it */
    components = BongoJsonNodeAsArray(componentsNode);
    BongoJsonNodeFreeSteal(componentsNode);

    tzs = GetTimezones(components);
    
    newObjs = BongoHashtableCreate(8, (HashFunction)BongoStringHash, (CompareFunction)strcmp);

    /* What's left is a bare object with toplevel object attributes
     * and timezones.  We'll dup this for each new object.  */

    ret = BongoArrayNew(sizeof (BongoJsonNode *), components->len);
    
    for(i = 0; i < components->len; i++) {
        BongoJsonObject *newObj;
        BongoJsonNode *componentNode;
        BongoArray *newComponents;
        const char *uid;
        const char *type;
        
        componentNode = BongoJsonArrayGet(components, i);
        if (!componentNode) {
            /* Removed timezone node, ignore */
            continue;
        }

        newObj = NULL;
        uid = NULL;

        if (BongoJsonNodeResolveString(componentNode, "type", &type) == BONGO_JSON_OK) {
            if (!strcmp(type, "timezone")) {
                continue;
            }
        }

        if (BongoJsonNodeResolveString(componentNode, "uid/value", &uid) == BONGO_JSON_OK) {
            newObj = BongoHashtableGet(newObjs, uid);
        }
        
        if (newObj) {
            BongoJsonObjectGetArray(newObj, "components", &newComponents);
        } else {
            newObj = BongoJsonObjectDup(obj);
            BongoJsonArrayAppendObject(ret, newObj);
            if (uid) {
                BongoHashtablePut(newObjs, (void*)uid, newObj);
            }
            newComponents = BongoArrayNew(sizeof(BongoJsonNode*), 1);
            BongoJsonObjectPutArray(newObj, "components", newComponents);
        }

        if (componentNode) {
            if (componentNode->type == BONGO_JSON_OBJECT) {
                AddTimezones(newComponents, tzs, BongoJsonNodeAsObject(componentNode));
                BongoJsonArrayAppend(newComponents, componentNode);
            } else {
                BongoJsonNodeFree(componentNode);
            }
        }

    }

    /* Now we don't need the old array or object */

    BongoHashtableDelete(tzs);
    BongoHashtableDelete(newObjs);
    
    /* Use BongoArrayFree to prevent freeing the reparented nodes */
    BongoArrayFree(components, TRUE);
    BongoJsonObjectFree(obj);

    return ret;
}

static BongoJsonResult
MergeObjectComponents(BongoJsonObject *dest, BongoJsonObject *src)
{
    BongoJsonNode *srcComponentsNode;
    BongoArray *srcComponents;
    BongoArray *destComponents;
    BongoJsonResult res;
    unsigned int i;
    
    if (((res = BongoJsonObjectGet(src, "components", &srcComponentsNode)) != BONGO_JSON_OK)) {
        DPRINT("no components object in source");
        return res;
    }

    if (srcComponentsNode->type != BONGO_JSON_ARRAY) {
        DPRINT("components isn't an array");

        return BONGO_JSON_BAD_TYPE;
    }

    srcComponents = BongoJsonNodeAsArray(srcComponentsNode);

    if ((res = BongoJsonObjectGetArray(dest, "components", &destComponents)) != BONGO_JSON_OK) {
        DPRINT("no components piece in dest");
        return res;
    }

    assert(srcComponents != destComponents);

    /* Copy components into the destination array */
    for(i = 0; i < srcComponents->len; i++) {
        BongoJsonNode *node;
        node = BongoJsonArrayGet(srcComponents, i);
        DPRINT("moving %d (%s) to new components array\n", i, BongoJsonNodeToString(node));

        if (node) {
            BongoJsonArrayAppend(destComponents, node);
        }
    }

    /* Clean up the old object */
    BongoJsonObjectRemoveSteal(src, "components");

    /* Don't need the components node anymore, but we want to free the
     * array ourselves */
    BongoJsonNodeFreeSteal(srcComponentsNode);

    /* Use BongoArrayFree to prevent freeing the reparented nodes */
    BongoArrayFree(srcComponents, TRUE);

    return BONGO_JSON_OK;
}

BongoJsonResult
BongoCalMerge(BongoJsonObject *dest, BongoJsonObject *src)
{
    BongoJsonResult res;
    BongoJsonObject *prodid;

    assert(dest != src);

    DPRINT("merging\n");

    res = MergeObjectComponents(dest, src);
    if (res != BONGO_JSON_OK) {
        BongoJsonObjectFree(src);
        return res;
    }

    /* Take the blame for this abomination */

    if (BongoJsonObjectGetObject(dest, "prodid", &prodid) != BONGO_JSON_OK) {
        prodid = BongoJsonObjectNew();
        BongoJsonObjectPutObject(dest, "prodid", prodid);
    }
    
    BongoJsonObjectPutString(prodid, "value", BONGO_CAL_BONGO_PRODID);

    BongoJsonObjectFree(src);

    return BONGO_JSON_OK;
}

