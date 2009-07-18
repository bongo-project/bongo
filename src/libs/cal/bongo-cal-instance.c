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
#include "bongo-cal-private.h"
#include <bongocal.h>
#include <libical/ical.h>

typedef struct {
    BongoCalRecurFrequency freq;
    const char *name;
} FreqNames;
    
static FreqNames freqNames[] = {
    { BONGO_CAL_RECUR_NONE, "" },
    { BONGO_CAL_RECUR_SECONDLY, "secondly" },
    { BONGO_CAL_RECUR_MINUTELY, "minutely" },
    { BONGO_CAL_RECUR_HOURLY, "hourly" },
    { BONGO_CAL_RECUR_DAILY, "daily" },
    { BONGO_CAL_RECUR_WEEKLY, "weekly" },
    { BONGO_CAL_RECUR_MONTHLY, "monthly" },
    { BONGO_CAL_RECUR_YEARLY, "yearly" },
};

static const char *dayNames[] = {
    "MO", "TU", "WE", "TH", "FR", "SA", "SU"
};    

static BongoCalTimezone *
TimezoneCb(const char *tzid, void *data)
{
    BongoCalInstance *inst = data;
    
    return BongoCalObjectGetTimezone(inst->cal, tzid);
}

/* Helper object for interpreting json calendar objects.  Uses some
 * helpers from libical, but libical data types shouldn't be exported  */

BongoCalInstance *
BongoCalInstanceNew(BongoCalObject *obj, BongoJsonObject *json)
{
    BongoCalInstance *inst;
    
    inst = MemMalloc0(sizeof(BongoCalInstance));
    
    BongoCalInstanceInit(inst, obj, json);

    return inst;
}

void
BongoCalInstanceInit(BongoCalInstance *inst, BongoCalObject *obj, BongoJsonObject *json)
{
    inst->cal = obj;
    
    if (json == NULL) {
        json = BongoJsonObjectNew();
        BongoJsonObjectPutString(json, "type", "event");
    }

    inst->json = json;
}

void
BongoCalInstanceFree(BongoCalInstance *inst, BOOL freeJson)
{
    BongoCalInstanceClear(inst, freeJson);
    MemFree(inst);
}


#if 0
static void
FreeInstanceArray(GArray *instances, BOOL freeJson) 
{
    unsigned int i;
    
    if (!instances) {
        return;
    }

    for (i = 0; i < instances->len; i++) {
        BongoCalInstanceFree(g_array_index(instances, BongoCalInstance*, i), freeJson);
    }
    
    g_array_free(instances, TRUE);
}
#endif

static void
FreeRRule(BongoCalRule *r)
{
    BongoListFree (r->bymonth);
    BongoListFree (r->byweekno);
    BongoListFree (r->byyearday);
    BongoListFree (r->bymonthday);
    BongoListFree (r->byday);
    BongoListFree (r->byhour);
    BongoListFree (r->byminute);
    BongoListFree (r->bysecond);
    BongoListFree (r->bysetpos);

    MemFree (r);
}

static void
FreeRRules(BongoSList *rules)
{
    BongoSList *l;
    
    for (l = rules; l != NULL; l = l->next) {
        FreeRRule(l->data);
    }

    BongoSListFree(rules);
}

void 
BongoCalInstanceClear(BongoCalInstance *inst, BOOL freeJson)
{
    if (freeJson) {
        BongoJsonObjectFree(inst->json);
    }

    BongoSListFreeDeep(inst->rdates);
    BongoSListFreeDeep(inst->exdates);
    FreeRRules(inst->rrules);
    FreeRRules(inst->exrules);
}

static void
ReadTime(BongoCalInstance *inst, BongoJsonObject *obj, BongoCalTime *time)
{
    const char *value;
    BongoJsonObject *params;
    
    if (BongoJsonObjectGetString(obj, "value", &value) != BONGO_JSON_OK) {
        return;
    }

    *time = BongoCalTimeParseIcal(value);

    if (BongoJsonObjectGetObject(obj, "params", &params) != BONGO_JSON_OK) {
        return;
    }

    if (BongoJsonObjectGetString(params, "tzid", &value) == BONGO_JSON_OK) {
        BongoCalTimezone *tz;

        tz = BongoCalObjectGetTimezone(inst->cal, value);
        if (tz) {
            *time = BongoCalTimeSetTimezone(*time, tz);
        } else {
            *time = BongoCalTimeSetTzid(*time, value);
        }
    } else {
        *time = BongoCalTimeSetTimezone(*time, BongoCalTimezoneGetUtc());
    }
    
    if (BongoJsonObjectGetString(params, "type", &value) == BONGO_JSON_OK) {
        if (!XplStrCaseCmp(value, "DATE")) {
            time->isDate = TRUE;
        } else if (!XplStrCaseCmp(value, "DATE-TIME")) {
            time->isDate = FALSE;
        }
    }
}

static BongoJsonObject *
WriteTime(BongoCalTime t)
{
    BongoJsonObject *obj;
    char buffer[BONGO_CAL_TIME_BUFSIZE];
    BongoJsonObject *params;
    BOOL haveParams;

    obj = BongoJsonObjectNew();

    params = BongoJsonObjectNew();
    haveParams = FALSE;
    
    if (!t.isUtc && t.tzid) {
        BongoJsonObjectPutString(params, "tzid", t.tzid);
        haveParams = TRUE;
    }

    if (t.isDate) {
        BongoJsonObjectPutString(params, "type", "DATE");
        haveParams = TRUE;        
    }

    if (haveParams) {
        BongoJsonObjectPutObject(obj, "params", params);
    } else {
        BongoJsonObjectFree(params);
    }

    BongoCalTimeToIcal(t, buffer, sizeof(buffer));
    BongoJsonObjectPutString(obj, "value", buffer);

    return obj;
}


static BongoJsonObject *
WriteLocalizedTime(BongoCalTime t, BongoCalTimezone *tz)
{
    BongoJsonObject *obj;
    
    obj = WriteTime(t);
    
    if (tz) {
        char buffer[BONGO_CAL_TIME_BUFSIZE];
        t = BongoCalTimezoneConvertTime(t, tz);
        BongoCalTimeToIcal(t, buffer, sizeof(buffer));
        BongoJsonObjectPutString(obj, "valueLocal", buffer);
    }

    return obj;
}

static BOOL
ReadPeriod(BongoCalInstance *inst, BongoJsonObject *obj, BongoCalPeriod *period)
{
    const char *value;
    BongoJsonObject *jsonValue;
    
    if (BongoJsonObjectGetObject(obj, "value", &jsonValue) != BONGO_JSON_OK) {
        return FALSE;
    }    
    
    if (BongoJsonObjectGetString(jsonValue, "start", &value) != BONGO_JSON_OK) {
        return FALSE;
    }

    period->start = BongoCalTimeParseIcal(value);

    if (BongoJsonObjectGetString(jsonValue, "end", &value) == BONGO_JSON_OK) {
        period->value.end = BongoCalTimeParseIcal(value);
        period->type = BONGO_CAL_PERIOD_DATETIME;
    } else if (BongoJsonObjectGetString(jsonValue, "duration", &value) == BONGO_JSON_OK) {
        period->value.duration = BongoCalDurationParseIcal(value);
        period->type = BONGO_CAL_PERIOD_DURATION;
    } else {
        return FALSE;
    }

    return TRUE;
}

static BongoCalRecurFrequency 
FindFrequency(const char *name)
{
    unsigned int i;
    
    for (i = 0; i < sizeof(freqNames) / sizeof(FreqNames); i++) {
        if (!XplStrCaseCmp(freqNames[i].name, name)) {
            return freqNames[i].freq;
        }
    }
    return BONGO_CAL_RECUR_NONE;
}

static int
GetWeekday(const char *name) 
{
    int i;
    for (i = 0; i < 7; i++) {
        if (!XplStrCaseCmp(dayNames[i], name)) {
            return i;
        }
    }
    return -1;
}

static BongoList *
IntArrayToList(GArray *array, int min, int max)
{
    BongoList *ret = NULL;
    unsigned int i;
    
    for (i = 0; i < array->len; i++) {
        int intVal;
        if (BongoJsonArrayGetInt(array, i, &intVal) == BONGO_JSON_OK) {
            if (intVal >= min && intVal <= max) {
                ret = BongoListPrepend(ret, (char *)(intVal));
            }
        }
    }
    
    return BongoListReverse(ret);
}

static BongoList *
DayArrayToList(GArray *array)
{
    BongoList *ret = NULL;
    unsigned int i;
    
    for (i = 0; i < array->len; i++) {
        const char *stringVal;
        if (BongoJsonArrayGetString(array, i, &stringVal) == BONGO_JSON_OK) {
            int ord;
            int day;
            char *end;

            ord = strtol(stringVal, &end, 10);
            day = GetWeekday(end);
            
            if (ord >= -53 && ord <= 53) {
                ret = BongoListPrepend(ret, (char *)(day));
                ret = BongoListPrepend(ret, (char *)(ord));
            }
        }
    }
    
    return BongoListReverse(ret);
}

static BOOL
ReadRecur(BongoCalInstance *inst, BongoJsonObject *obj, BongoCalRule *recur)
{
    GArray *arrayVal;
    const char *stringVal;
    int intVal;
    BongoJsonObject *jsonValue;
    BongoJsonObject *jsonParams;

    if (BongoJsonObjectGetObject(obj, "value", &jsonValue) != BONGO_JSON_OK) {
        return FALSE;
    }

    BongoJsonObjectGetObject(obj, "params", &jsonParams);
    
    memset(recur, 0, sizeof(BongoCalRule));

    recur->json = obj;

    if (BongoJsonObjectGetString(jsonValue, "freq", &stringVal) != BONGO_JSON_OK) {
        return FALSE;
    }

    recur->freq = FindFrequency(stringVal);

    if (BongoJsonObjectGetInt(jsonValue, "interval", &intVal) == BONGO_JSON_OK) {
        recur->interval = intVal;
    }

    if (recur->interval == 0) {
        recur->interval = 1;
    }

    if (BongoJsonObjectGetString(jsonValue, "until", &stringVal) == BONGO_JSON_OK) {
        BongoCalTime t = BongoCalTimeParseIcal(stringVal);
        recur->until = BongoCalTimeAsUint64(t);
    } else if (BongoJsonObjectGetInt(jsonValue, "count", &intVal) == BONGO_JSON_OK) {
        recur->count = intVal;
        if (jsonParams && (BongoJsonObjectGetString(jsonParams, "x-bongo-until", &stringVal) == BONGO_JSON_OK)) {
            BongoCalTime t = BongoCalTimeParseIcal(stringVal);
            recur->until = BongoCalTimeAsUint64(t);
        }
    } 


    if (BongoJsonObjectGetString(jsonValue, "weekstart", &stringVal) == BONGO_JSON_OK) {
        recur->weekStartDay = GetWeekday(stringVal);
        if (recur->weekStartDay == -1) {
            return FALSE;
        }
    }

    if (BongoJsonObjectGetArray(jsonValue, "bymonth", &arrayVal) == BONGO_JSON_OK) {
        BongoList *l;
        
        recur->bymonth = IntArrayToList(arrayVal, 1, 12);
        
        for (l = recur->bymonth; l != NULL; l = l->next) {
            l->data = ((int)(l->data)) - 1;
        }
    }

    if (BongoJsonObjectGetArray(jsonValue, "byweekno", &arrayVal) == BONGO_JSON_OK) {
        recur->byweekno = IntArrayToList(arrayVal, -53, 53);
    }

    if (BongoJsonObjectGetArray(jsonValue, "byyearday", &arrayVal) == BONGO_JSON_OK) {
        recur->byyearday = IntArrayToList(arrayVal, -366, 366);
    }

    if (BongoJsonObjectGetArray(jsonValue, "bymonthday", &arrayVal) == BONGO_JSON_OK) {
        recur->bymonthday = IntArrayToList(arrayVal, 1, 31);
    }

    if (BongoJsonObjectGetArray(jsonValue, "byhour", &arrayVal) == BONGO_JSON_OK) {
        recur->byhour = IntArrayToList(arrayVal, 0, 23);
    }

    if (BongoJsonObjectGetArray(jsonValue, "byminute", &arrayVal) == BONGO_JSON_OK) {
        recur->byminute = IntArrayToList(arrayVal, 0, 59);
    }

    if (BongoJsonObjectGetArray(jsonValue, "bysecond", &arrayVal) == BONGO_JSON_OK) {
        recur->bysecond = IntArrayToList(arrayVal, 0, 60);
    }

    if (BongoJsonObjectGetArray(jsonValue, "bysetpos", &arrayVal) == BONGO_JSON_OK) {
        recur->bysetpos = IntArrayToList(arrayVal, INT_MIN, INT_MAX);
    }

    if (BongoJsonObjectGetArray(jsonValue, "byday", &arrayVal) == BONGO_JSON_OK) {
        recur->byday = DayArrayToList(arrayVal);
    }

    return TRUE;
}

static void
CacheTime(BongoCalInstance *inst, const char *id, BongoCalTime *time)
{
    BongoJsonObject *child;

    if (!BongoCalTimeIsEmpty(*time)) {
        return;    
    }
    
    if (BongoJsonObjectGetObject(inst->json, id, &child) != BONGO_JSON_OK) {
        return;
    }

    ReadTime(inst, child, time);
}

static void
CacheDuration(BongoCalInstance *inst, const char *id, BongoCalDuration *duration)
{
    BongoJsonObject *child;
    const char *value;

    if (!BongoCalDurationIsEmpty(*duration)) {
        return;    
    }
    
    if (BongoJsonObjectGetObject(inst->json, id, &child) != BONGO_JSON_OK) {
        return;
    }

    if (BongoJsonObjectGetString(child, "value", &value) != BONGO_JSON_OK) {
        return;
    }

    *duration = BongoCalDurationParseIcal(value);    
}

static void
SetTime(BongoCalInstance *inst, const char *id, BongoCalTime *cache, BongoCalTime t)
{
    BongoJsonObject *obj;
    
    obj = WriteTime(t);
    
    BongoJsonObjectPutObject(inst->json, id, obj);
    
    *cache = t;
}


static void
CacheString(BongoCalInstance *inst, const char *id, const char **cache) 
{
    BongoJsonObject *child;

    if (*cache) {
        return;    
    }
    
    if (BongoJsonObjectGetObject(inst->json, id, &child) != BONGO_JSON_OK) {
        return;
    }

    BongoJsonObjectGetString(child, "value", cache);
}

static void
SetString(BongoCalInstance *inst, const char *id, const char **cache, const char *value)
{
    BongoJsonObject *obj;
    
    obj = BongoJsonObjectNew();

    BongoJsonObjectPutString(obj, "value", value);
    BongoJsonObjectGetString(obj, "value", cache);
    
    BongoJsonObjectPutObject(inst->json, id, obj);
}


#if 0
static void
CacheInt(BongoCalInstance *inst, const char *id, BOOL *haveCache, int *cache) 
{
    BongoJsonObject *child;

    if (*haveCache) {
        return;
    }
    
    if (BongoJsonObjectGetObject(inst->json, id, &child) != BONGO_JSON_OK) {
        return;
    }
    
    if (BongoJsonObjectGetInt(child, "value", cache) == BONGO_JSON_OK) {
        *haveCache = TRUE;
    } else {
        *cache = 0;
    }
}
#endif

static void
CacheUtcOffset(BongoCalInstance *inst, const char *id, BOOL *haveCache, int *cache)
{
    BongoJsonObject *child;
    const char *str;

    if (*haveCache) {
        return;
    }
    
    if (BongoJsonObjectGetObject(inst->json, id, &child) != BONGO_JSON_OK) {
        return;
    }
    
    if (BongoJsonObjectGetString(child, "value", &str) == BONGO_JSON_OK) {
        int t;
        int hours;
        int minutes;
        int seconds;
        
        *haveCache = TRUE;

        t = strtol(str, 0, 10);
        if (abs(t) < 9999) {
            t *= 100;
        }
        hours = (t / 10000);
        minutes = (t - hours * 10000) / 100;
        seconds = (t - hours * 10000 - minutes * 100);
        *cache = hours * 3600 + minutes * 60 + seconds;
    } else {
        *cache = 0;
    }
}

static void
CacheRDates(BongoCalInstance *inst, const char *key, BongoSList **dates)
{
    GArray *array;
    unsigned int i;
    
    if (*dates) {
        return;
    }

    if (BongoJsonObjectGetArray(inst->json, key, &array) != BONGO_JSON_OK) {
        return;
    }

    for (i = 0; i < array->len; i++) {
        BongoJsonObject *obj;
        if (BongoJsonArrayGetObject(array, i, &obj) == BONGO_JSON_OK) {
            BongoCalRDate *rdate;
            const char *type;
            BOOL period = FALSE;

            if (BongoJsonObjectGetString(obj, "type", &type) == BONGO_JSON_OK) {
                if (!XplStrCaseCmp(type, "period")) {
                    period = TRUE;                    
                }
            }

            rdate = MemNew0(BongoCalRDate, 1);

            if (period) {
                rdate->type = BONGO_CAL_RDATE_PERIOD;
                ReadPeriod(inst, obj, &rdate->value.period);
            } else {
                rdate->type = BONGO_CAL_RDATE_DATETIME;
                ReadTime(inst, obj, &rdate->value.time);
            }

            *dates = BongoSListPrepend(*dates, rdate);
        }
    }
    *dates = BongoSListReverse(*dates);
}

static void
CacheRRules(BongoCalInstance *inst, const char *key, BongoSList **rules)
{
    GArray *array;
    unsigned int i;
    
    if (*rules) {
        return;
    }

    if (BongoJsonObjectGetArray(inst->json, key, &array) != BONGO_JSON_OK) {
        return;
    }

    for (i = 0; i < array->len; i++) {
        BongoJsonObject *obj;
        if (BongoJsonArrayGetObject(array, i, &obj) == BONGO_JSON_OK) {
            BongoCalRule *r;
            
            r = MemNew0(BongoCalRule, 1);
            
            ReadRecur(inst, obj, r);

            *rules = BongoSListPrepend(*rules, r);
        }
    }
    *rules = BongoSListReverse(*rules);
}

static void
EnsureUid(BongoCalInstance *inst)
{
    CacheString(inst, "uid", &inst->uid);
    if (!inst->uid) {
        SetString(inst, "uid", &inst->uid, "NO UID DEFINED");
    }
}

BongoCalType
BongoCalInstanceGetType(BongoCalInstance *instance)
{
    return BongoCalTypeFromJson(instance->json);
}

BongoJsonObject *
BongoCalInstanceGetJson(BongoCalInstance *inst)
{
    return inst->json;
}

const char *
BongoCalInstanceGetUid(BongoCalInstance *inst)
{
    EnsureUid(inst);
    return inst->uid;
}

void
BongoCalInstanceSetUid(BongoCalInstance *inst, const char *uid)
{
    SetString(inst, "uid", &inst->uid, uid);
}

BongoCalTime 
BongoCalInstanceGetRecurId(BongoCalInstance *inst)
{
    CacheTime(inst, "recurid", &inst->recurid);
    return inst->recurid;
}

void
BongoCalInstanceSetRecurId(BongoCalInstance *inst, BongoCalTime recurid)
{
    SetTime(inst, "recurid", &inst->recurid, recurid);
}

BOOL
BongoCalInstanceIsRecurrence(BongoCalInstance *instance)
{
    BongoJsonObject *obj;
    return (BongoJsonObjectGetObject(instance->json, "recurid", &obj) == BONGO_JSON_OK);
}

const char *
BongoCalInstanceGetSummary(BongoCalInstance *inst)
{
    CacheString(inst, "summary", &inst->summary);
    return inst->summary;
}

void
BongoCalInstanceSetSummary(BongoCalInstance *inst, const char *summary)
{
    SetString(inst, "summary", &inst->summary, summary);
}

const char *
BongoCalInstanceGetStamp(BongoCalInstance *inst)
{
    CacheString(inst, "dtstamp", &inst->stamp);
    return inst->stamp;
}

void
BongoCalInstanceSetStamp(BongoCalInstance *inst, const char *stamp)
{
    SetString(inst, "dtstamp", &inst->stamp, stamp);
}

const char *
BongoCalInstanceGetLocation(BongoCalInstance *inst)
{
    CacheString(inst, "location", &inst->location);
    return inst->location;
}

const char *
BongoCalInstanceGetDescription(BongoCalInstance *inst)
{
    CacheString(inst, "description", &inst->description);
    return inst->description;
}

BOOL 
BongoCalInstanceHasStart(BongoCalInstance *inst) 
{
    CacheTime(inst, "dtstart", &inst->start);
    return !BongoCalTimeIsEmpty(inst->start);
}

BongoCalTime 
BongoCalInstanceGetStart(BongoCalInstance *inst)
{
    CacheTime(inst, "dtstart", &inst->start);
    return inst->start;
}

void 
BongoCalInstanceSetStart(BongoCalInstance *inst, BongoCalTime t)
{
    SetTime(inst, "dtstart", &inst->start, t);
}

BOOL 
BongoCalInstanceHasEnd(BongoCalInstance *inst) 
{
    /* FIXME: deal with Duration time */
    CacheTime(inst, "dtend", &inst->end);
    return BongoCalTimeIsEmpty(inst->end);
}

BongoCalTime 
BongoCalInstanceGetEnd(BongoCalInstance *inst)
{
    CacheTime(inst, "dtend", &inst->end);
    if (BongoCalTimeIsEmpty(inst->end)) {
        CacheDuration(inst, "duration", &inst->duration);
        /* FIXME: deal with Duration time */
        inst->end = BongoCalTimeAdjustDuration(inst->start, inst->duration);
    }

    return inst->end;
}

/* Returns the last recurrence end time */
BongoCalTime 
BongoCalInstanceGetLastEnd(BongoCalInstance *instance)
{
    BongoSList *rrules;
    BongoCalTime lastRecur;
    BongoCalTime end;
    BOOL done;
    
    end = BongoCalInstanceGetEnd(instance);

    if (!BongoCalInstanceHasRecurrences(instance)) {
        return end;
    }

    BongoCalRecurEnsureEndDates(instance, FALSE, TimezoneCb, instance);

    done = FALSE;
    lastRecur = BongoCalTimeEmpty();

    if (BongoCalInstanceGetRRules(instance, &rrules)) {
        BongoSList *l;
        for (l = rrules; l != NULL; l = l->next) {
            BongoCalRule *recur = l->data;
            BongoCalTime untilt;
            
            if (recur->count == 0 && recur->until == 0) {
                /* Infinitely-recurring rule, lastend is MAX_RECUR */
                lastRecur = BongoCalTimeEmpty();
                lastRecur.year = MAX_YEAR;
                done = TRUE;
                break;
            }

            untilt = BongoCalTimeNewFromUint64(recur->until, FALSE, NULL);

            if (BongoCalTimeCompare(untilt, lastRecur) > 0) {
                lastRecur = untilt;
            }
        }
    }

    if (BongoCalTimeCompare(lastRecur, end) < 0) {
        return end;
    } else {
        /* FIXME: we really want to use the length of the appt here */
        return BongoCalTimeAdjust(lastRecur, 1, 0, 0, 0);
    }
}

void 
BongoCalInstanceSetEnd(BongoCalInstance *inst, BongoCalTime t)
{
    SetTime(inst, "dtend", &inst->end, t);
}

BOOL
BongoCalInstanceHasTzOffsetTo(BongoCalInstance *inst)
{
    CacheUtcOffset(inst, "tzoffsetto", &inst->hasTzOffsetTo, &inst->tzOffsetTo);
    return inst->hasTzOffsetTo;
}

int 
BongoCalInstanceGetTzOffsetTo(BongoCalInstance *inst)
{
    CacheUtcOffset(inst, "tzoffsetto", &inst->hasTzOffsetTo, &inst->tzOffsetTo);
    return inst->tzOffsetTo;
}

BOOL
BongoCalInstanceHasTzOffsetFrom(BongoCalInstance *inst)
{
    CacheUtcOffset(inst, "tzoffsetfrom", &inst->hasTzOffsetFrom, &inst->tzOffsetFrom);
    return inst->hasTzOffsetFrom;
}

int 
BongoCalInstanceGetTzOffsetFrom(BongoCalInstance *inst)
{
    CacheUtcOffset(inst, "tzoffsetfrom", &inst->hasTzOffsetFrom, &inst->tzOffsetFrom);
    return inst->tzOffsetFrom;
}

BOOL
BongoCalInstanceHasRecurrences(BongoCalInstance *inst)
{
    return (BongoCalInstanceHasRDates(inst) || BongoCalInstanceHasRRules(inst));
}

BOOL
BongoCalInstanceHasRRules(BongoCalInstance *inst)
{
    CacheRRules(inst, "rrules", &inst->rrules);
    return (inst->rrules != NULL);
}

BOOL 
BongoCalInstanceGetRRules(BongoCalInstance *inst, BongoSList **list)
{
    CacheRRules(inst, "rrules", &inst->rrules);
    *list = inst->rrules;
    return (inst->rrules != NULL);
}

BOOL
BongoCalInstanceHasRDates(BongoCalInstance *inst)
{    
    CacheRDates(inst, "rdates", &inst->rdates);

    return (inst->rdates != NULL);
}

BOOL
BongoCalInstanceGetRDates(BongoCalInstance *inst, BongoSList **list)
{
    CacheRDates(inst, "rdates", &inst->rdates);
    *list = inst->rdates;
    return (inst->rdates != NULL);
}

/* Recurrence exceptions */

BOOL
BongoCalInstanceHasExceptions(BongoCalInstance *inst)
{
    return BongoCalInstanceHasExRules(inst) || BongoCalInstanceHasExDates(inst);
}

BOOL
BongoCalInstanceHasExRules(BongoCalInstance *inst)
{
    CacheRRules(inst, "exrules", &inst->exrules);
    return (inst->exrules != NULL);    
}

BOOL
BongoCalInstanceGetExRules(BongoCalInstance *inst, BongoSList **list)
{
    CacheRRules(inst, "exrules", &inst->exrules);
    *list = inst->exrules;
    return (inst->exrules != NULL);    
}

BOOL
BongoCalInstanceHasExDates(BongoCalInstance *inst)
{
    CacheRDates(inst, "exdates", &inst->exdates);
    return (inst->exdates != NULL);
}

BOOL
BongoCalInstanceGetExDates(BongoCalInstance *inst, BongoSList **list)
{
    CacheRDates(inst, "exdates", &inst->exdates);
    *list = inst->exdates;
    return (inst->exdates != NULL);
}


BongoCalOccurrence 
BongoCalInstanceGetOccurrence(BongoCalInstance *instance)
{
    BongoCalOccurrence ret;
    
    ret.start = BongoCalInstanceGetStart(instance);
    ret.end = BongoCalInstanceGetEnd(instance);
    ret.recurid = BongoCalInstanceGetRecurId(instance);
    ret.generated = FALSE;
    ret.instance = instance;

    return ret;
}


#if 0
BOOL
BongoCalInstanceGetInstance(BongoCalInstance *inst, 
                           BongoCalTime recurid, 
                           BOOL allowGenerated,
                           BongoCalInstance **instOut)
{
    int i;
    
    CacheInstances(inst, "generated", &inst->generated, &inst->generatedSorted);
    CacheInstances(inst, "exceptions", &inst->exceptions, &inst->exceptionsSorted);

    if (inst->exceptions) {
        if (!inst->exceptionsSorted) {
            inst->exceptionsSorted = TRUE;
        }

        i = GArrayFindSorted(inst->exceptions, &recurid, FindRecurId);    
        if (i != -1) {
            *instOut = g_array_index(inst->exceptions, BongoCalInstance *, i);
            return TRUE;
        }
    }

    if (inst->generated && allowGenerated) {
        if (!inst->generatedSorted) {
            g_array_sort(inst->generated, CompareRecurId);
            inst->generatedSorted = TRUE;
        }

        i = GArrayFindSorted(inst->generated, &recurid, FindRecurId);
        if (i != -1) {
            *instOut = g_array_index(inst->generated, BongoCalInstance *, i);
            return TRUE;
        }
    }

    *instOut = NULL;
    return FALSE;
}

static void
AddGeneratedInstance(BongoCalInstance *inst,
                     BongoCalInstance *child,
                     BOOL isGenerated)
{        
    GArray *array;
   
    /* Requires "generated" to be created first */

    if (BongoJsonObjectGetArray(inst->json, "generated", &array) != BONGO_JSON_OK) {
        array = g_array_sized_new(FALSE, FALSE, sizeof(BongoJsonNode *), 15);
        BongoJsonObjectPutArray(inst->json, "generated", array);
    }

    if (array) {
        BongoJsonArrayAppendObject(array, child->json);

        if (!inst->generated) {
            inst->generated = g_array_sized_new(FALSE, FALSE, sizeof(BongoCalInstance *), 16);
        }
        
        g_array_append_val(inst->generated, child);
        inst->generatedSorted = FALSE;
    }
}
#endif


BOOL
BongoCalInstanceCrosses(BongoCalInstance *instance,
                       BongoCalTime start,
                       BongoCalTime end)
{
    BongoCalTime instStart;
    BongoCalTime instEnd;
    
    instStart = BongoCalInstanceGetStart(instance);
    instEnd = BongoCalInstanceGetEnd(instance);

    if (BongoCalTimeCompare(instStart, end) <= 0 &&
        BongoCalTimeCompare(instEnd, start) >= 0) {
        return TRUE;
    }

    return FALSE;
}

static int
CompareOccs(const void *voida, const void *voidb)
{
    BongoCalOccurrence *occa = (BongoCalOccurrence*)voida;
    BongoCalOccurrence *occb = (BongoCalOccurrence*)voidb;
    int ret;

    ret = BongoCalTimeCompare(occa->start, occb->start);
    if (ret != 0) {
        return ret;
    }

    ret = BongoCalTimeCompare(occa->end, occb->end);
    if (ret != 0) {
        return ret;
    }

    return strcmp(BongoCalInstanceGetUid(occa->instance), BongoCalInstanceGetUid(occb->instance));
}

void
BongoCalOccurrencesSort(GArray *occurrences)
{
    g_array_sort(occurrences, CompareOccs);
}

BongoJsonObject *
BongoCalOccurrenceToJson(BongoCalOccurrence occ, BongoCalTimezone *tz)
{
    BongoJsonObject *obj;
    BongoJsonObject *child;
    BongoJsonNode *summary;
    
    obj = BongoJsonObjectNew();

    child = WriteLocalizedTime(occ.start, tz);
    if (BongoJsonObjectPutObject(obj, "dtstart", child) != BONGO_JSON_OK) {
        BongoJsonObjectFree(child);
        BongoJsonObjectFree(obj);
        return NULL;
    }

    child = WriteLocalizedTime(occ.end, tz);
    if (BongoJsonObjectPutObject(obj, "dtend", child) != BONGO_JSON_OK) {
        BongoJsonObjectFree(child);
        BongoJsonObjectFree(obj);
        return NULL;
    }

    if (!BongoCalTimeIsEmpty(occ.recurid)) {
        child = WriteTime(occ.recurid);
        if (BongoJsonObjectPutObject(obj, "recurid", child) != BONGO_JSON_OK) {
            BongoJsonObjectFree(child);
            BongoJsonObjectFree(obj);
            return NULL;
        }
    }

    if (BongoJsonObjectPutString(obj, "uid", BongoCalInstanceGetUid(occ.instance)) != BONGO_JSON_OK) {
        BongoJsonObjectFree(obj);
        return NULL;
    }

    if (BongoJsonObjectGet(occ.instance->json, "summary", &summary) == BONGO_JSON_OK) {
        BongoJsonNode *dup;
        dup = BongoJsonNodeDup(summary);
        if (dup) {
            BongoJsonObjectPut(obj, "summary", dup);
        }
    }

    if (BongoJsonObjectPutBool(obj, "generated", occ.generated) != BONGO_JSON_OK) {
        BongoJsonObjectFree(obj);
        return NULL;
    }
    
    return obj;
}

GArray *
BongoCalOccurrencesToJson(GArray *occs, BongoCalTimezone *tz)
{
    GArray *jsonArray;
    unsigned int i;
    
    jsonArray = g_array_sized_new(FALSE, FALSE, sizeof(BongoJsonNode *), 16);
    
    for (i = 0; i < occs->len; i++) {
        BongoJsonObject *obj;
        
        obj = BongoCalOccurrenceToJson(g_array_index(occs, BongoCalOccurrence, i), tz);
        
        if (obj) {
            BongoJsonArrayAppendObject(jsonArray, obj);
        }
    }
    
    return jsonArray;
}
