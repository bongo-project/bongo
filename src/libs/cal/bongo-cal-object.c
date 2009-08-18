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
#include <bongoutil.h>
#include <bongocal.h>
#include <ical-wrapper.h>
#include <bongocal-raw.h>
#include "bongo-cal-private.h"

typedef struct { 
    const char *name;
    BongoCalMethod method;
} MethodName;

typedef struct {
    const char *uid;
    BongoCalTime recurid;
} IdSearchData;

typedef struct {
    BongoCalObject *cal;
    GArray *occs;
} CollectData;

static MethodName methodNames[] = {
    { "PUBLISH", BONGO_CAL_METHOD_PUBLISH },
    { "REQUEST", BONGO_CAL_METHOD_REQUEST },
    { "REPLY", BONGO_CAL_METHOD_REPLY },
    { "ADD", BONGO_CAL_METHOD_ADD },
    { "CANCEL", BONGO_CAL_METHOD_CANCEL },
    { "REFRESH", BONGO_CAL_METHOD_REFRESH },
    { "COUNTER", BONGO_CAL_METHOD_COUNTER },
    { "DECLINECOUNTER", BONGO_CAL_METHOD_DECLINECOUNTER },
    { "CREATE", BONGO_CAL_METHOD_CREATE },
    { "READ", BONGO_CAL_METHOD_READ },
    { "RESPONSE", BONGO_CAL_METHOD_RESPONSE },
    { "MOVE", BONGO_CAL_METHOD_MOVE },
    { "MODIFY", BONGO_CAL_METHOD_MODIFY },
    { "GENERATEUID", BONGO_CAL_METHOD_GENERATEUID },
    { "DELETE", BONGO_CAL_METHOD_DELETE },
};

static int
CompareInstancesId(const void *voida, const void *voidb)
{
    BongoCalInstance *a = *((BongoCalInstance**)voida);
    BongoCalInstance *b = *((BongoCalInstance**)voidb);
    const char *uida;
    const char *uidb;
    BongoCalTime recura;
    BongoCalTime recurb;
    int ret;
    
    uida = BongoCalInstanceGetUid(a);
    uidb = BongoCalInstanceGetUid(b);
    
    ret = strcmp(uida, uidb);
    
    if (ret != 0) {
        return ret;
    }

    recura = BongoCalInstanceGetRecurId(a);
    recurb = BongoCalInstanceGetRecurId(b);
    
    return BongoCalTimeCompare(recura, recurb);
}


static int
FindInstanceById(const void *voida, const void *voidb)
{
    IdSearchData *data = (IdSearchData *)voida;
    BongoCalInstance *inst = *((BongoCalInstance**)voidb);
    int ret;

    ret = strcmp(data->uid, BongoCalInstanceGetUid(inst));
    
    if (ret != 0) {
        return ret;
    }
    
    return BongoCalTimeCompare(data->recurid, BongoCalInstanceGetRecurId(inst));
}

static void
FindInstances(BongoCalObject *cal)
{
    GArray *components;
    unsigned int i;
    
    if (BongoJsonObjectGetArray(cal->json, "components", &components) != BONGO_JSON_OK) {
        return;
    }
    
    for (i = 0; i < components->len; i++) {
        BongoJsonObject *object;
        
        if (BongoJsonArrayGetObject(components, i, &object) == BONGO_JSON_OK) {
            const char *type;
            
            if (BongoJsonObjectGetString(object, "type", &type) != BONGO_JSON_OK) {
                type = "event";
            }

            if (!XplStrCaseCmp(type, "timezone")) {
                BongoCalTimezone *tz = BongoCalTimezoneNewJson(cal, object, NULL);
                BongoHashtablePut(cal->timezones, (char*)BongoCalTimezoneGetTzid(tz), tz);
            } else {
                BongoCalInstance *inst = BongoCalInstanceNew(cal, object);
                g_array_append_val(cal->instances, inst);
            }
        }
    }

    g_array_sort(cal->instances, CompareInstancesId);
    cal->instancesSorted = TRUE;
}

static void
FreeTimezone(void *value)
{
    BongoCalTimezoneFree(value, FALSE);
}

static BOOL
AddValueObject(BongoJsonObject *obj, const char *key, const char *value)
{
    BongoJsonObject *child;
    
    child = BongoJsonObjectNew();
    if (!child) {
        return FALSE;
    }
    
    if (BongoJsonObjectPutString(child, "value", value) != BONGO_JSON_OK) {
        BongoJsonObjectFree(child);
        return FALSE;
    }

    if (BongoJsonObjectPutObject(obj, key, child) != BONGO_JSON_OK) {
        BongoJsonObjectFree(child);
        return FALSE;
    }
    
    return TRUE;
}

static BongoJsonObject *
NewCalJson(void)
{
    BongoJsonObject *json;
    
    json = BongoJsonObjectNew();
    GArray *array = g_array_new(FALSE, FALSE, sizeof(BongoJsonNode *));

    if (json && array &&
        AddValueObject(json, "version", "2.0") &&
        AddValueObject(json, "prodid", BONGO_CAL_BONGO_PRODID) &&
        BongoJsonObjectPutArray(json, "components", array) == BONGO_JSON_OK) {
        return json;
    } else {
        if (array) {
            g_array_free(array, TRUE);
        }
        
        if (json) {
            BongoJsonObjectFree(json);
        }
        return NULL;
    }
}


BongoCalObject *
BongoCalObjectNew(BongoJsonObject *json)
{
    BongoCalObject *cal;
    
    cal = MemMalloc0(sizeof(BongoCalObject));
    
    if (json == NULL) {
        json = NewCalJson();
    }

    cal->json = json;

    cal->instances = g_array_sized_new(FALSE, FALSE, sizeof(BongoCalInstance *), 10);
    cal->timezones = BongoHashtableCreateFull(15, 
                                             (HashFunction)BongoStringHash,
                                             (CompareFunction)strcmp,
                                             NULL,
                                             FreeTimezone);
    FindInstances(cal);

    return cal;
}

static BongoCalObject *
BongoCalObjectNewNode(BongoJsonNode *node)
{
    BongoCalObject *cal;

    if (node && node->type == BONGO_JSON_OBJECT) {
        BongoJsonObject *json;
        
        json = BongoJsonNodeAsObject(node);

        cal = BongoCalObjectNew(json);

        if (cal) {
            BongoJsonNodeFreeSteal(node);
            node = NULL;
        }
    } else {
        cal = NULL;
    }

    if (node) {
        BongoJsonNodeFree(node);
    }
    
    return cal;
}

BongoCalObject *
BongoCalObjectParseString(const char *str)
{
    BongoJsonNode *node;
    
    if (BongoJsonParseString(str, &node) == BONGO_JSON_OK) {
        return BongoCalObjectNewNode(node);
    } else {
        return NULL;
    }
}

static BongoCalObject *
BongoCalObjectNewIcal(icalcomponent *component)
{
    BongoJsonObject *json;
    
    if (component) {
        icalcomponent_strip_errors(component);
        json = BongoCalIcalToJson(component);
        icalcomponent_free(component);
        if (json) {
            return BongoCalObjectNew(json);
        }
    }

    return NULL;
}

BongoCalObject *
BongoCalObjectParseIcalString(const char *str)
{
    icalcomponent *comp;
    
    comp = icalparser_parse_string(str);
    return BongoCalObjectNewIcal(comp);
}

BongoCalObject *
BongoCalObjectParseFile(FILE *f, int toRead)
{
    BongoJsonNode *node;
    
    if (BongoJsonParseFile(f, toRead, &node) == BONGO_JSON_OK) {
        return BongoCalObjectNewNode(node);
    } else {
        return NULL;
    }
}

void
BongoCalObjectFree(BongoCalObject *cal, BOOL freeJson)
{
    unsigned int i;

    for (i = 0; i < cal->instances->len; i++) {
        BongoCalInstanceFree(g_array_index(cal->instances, BongoCalInstance *, i), FALSE);
    }

    g_array_free(cal->instances, TRUE);

    if (freeJson) {
        BongoJsonObjectFree(cal->json);
    }

    BongoHashtableDelete(cal->timezones);
    
    MemFree(cal);
}

BongoJsonObject *
BongoCalObjectGetJson(BongoCalObject *cal)
{
    return cal->json;
}

BongoCalMethod 
BongoCalObjectGetMethod(BongoCalObject *cal)
{
    const char *method;
    
    if (cal->methodRead) {
        return cal->method;
    }
    
    cal->methodRead = TRUE;

    if (BongoJsonObjectResolveString(cal->json, "method/value", &method) == BONGO_JSON_OK) {
        unsigned int i;
        for (i = 0; i < sizeof(methodNames) / sizeof(MethodName); i++) {
            if (!XplStrCaseCmp(methodNames[i].name, method)) {
                cal->method = methodNames[i].method;
                return cal->method;
            }
        }
        return BONGO_CAL_METHOD_UNKNOWN;
    } else {
        return BONGO_CAL_METHOD_NONE;
    }
}

void
BongoCalObjectSetMethod(BongoCalObject *cal, BongoCalMethod method)
{
    if (method == BONGO_CAL_METHOD_NONE) {
        BongoJsonObjectRemove(cal->json, "method");
    } else {
        unsigned int i;
        for (i = 0; i < sizeof(methodNames) / sizeof(MethodName); i++) {
            if (methodNames[i].method == method) {
                BongoJsonObject *obj;
                obj = BongoJsonObjectNew();
                BongoJsonObjectPutString(obj, "value", methodNames[i].name);
                BongoJsonObjectPutObject(cal->json, "method", obj);
                break;
            }
        }
    }
}

const char *
BongoCalObjectGetCalendarName(BongoCalObject *cal)
{
    if (cal->calName) {
        return cal->calName;
    }
    
    if (BongoJsonObjectResolveString(cal->json, "X-WR-CALNAME/0/value", &cal->calName) == BONGO_JSON_OK) {
        return cal->calName;
    }
    return NULL;
}

const char *
BongoCalObjectGetUid(BongoCalObject *cal)
{
    
    if (cal->instances->len > 0) {
        return BongoCalInstanceGetUid(g_array_index(cal->instances, BongoCalInstance *, 0));
    }
    return NULL;
}

const char *
BongoCalObjectGetStamp(BongoCalObject *cal)
{
    
    if (cal->instances->len > 0) {
        return BongoCalInstanceGetStamp(g_array_index(cal->instances, BongoCalInstance *, 0));
    }
    return NULL;
}

BongoCalTime
BongoCalObjectGetStart(BongoCalObject *cal)
{
    BongoCalTime start;
    BOOL first = TRUE;
    unsigned int i;

    start = BongoCalTimeEmpty();
    
    for (i = 0; i < cal->instances->len; i++) {
        BongoCalInstance *inst = g_array_index(cal->instances, BongoCalInstance *, i);
        BongoCalTime instStart;
        
        instStart = BongoCalInstanceGetStart(inst);
        
        if (first || BongoCalTimeCompare(start, instStart) < 0) {
            start = instStart;
            first = FALSE;
        }
    }    

    return start;
}

BongoCalTime
BongoCalObjectGetEnd(BongoCalObject *cal)
{
    BongoCalTime end;
    BOOL first = TRUE;
    unsigned int i;

    end = BongoCalTimeEmpty();
    
    for (i = 0; i < cal->instances->len; i++) {
        BongoCalInstance *inst = g_array_index(cal->instances, BongoCalInstance *, i);
        BongoCalTime instEnd;
        char buffer[1024];
        
        instEnd = BongoCalInstanceGetLastEnd(inst);

        BongoCalTimeToIcal(instEnd, buffer, 1024);
        
        if (first || BongoCalTimeCompare(end, instEnd) < 0) {
            end = instEnd;
            first = FALSE;
        }
    }

    return end;
}

const char *
BongoCalObjectGetSummary(BongoCalObject *cal)
{
    if (cal->instances->len < 1) {
        return NULL;
    }
    
    return BongoCalInstanceGetSummary(g_array_index(cal->instances, BongoCalInstance *, 0));
}

const char *
BongoCalObjectGetLocation(BongoCalObject *cal)
{
    if (cal->instances->len < 1) {
        return NULL;
    }
    
    return BongoCalInstanceGetLocation(g_array_index(cal->instances, BongoCalInstance *, 0));
}

const char *
BongoCalObjectGetDescription(BongoCalObject *cal)
{
    if (cal->instances->len < 1) {
        return NULL;
    }
    
    return BongoCalInstanceGetDescription(g_array_index(cal->instances, BongoCalInstance *, 0));
}

BongoCalTimezone *
BongoCalObjectGetTimezone(BongoCalObject *cal, const char *tzid)
{
    BongoCalTimezone *tz;
    
    tz = BongoCalTimezoneGetSystem(tzid);
    if (tz) {
        return tz;
    }

    tz = BongoHashtableGet(cal->timezones, tzid);
    
    return tz;
}

BongoHashtable *
BongoCalObjectGetTimezones(BongoCalObject *cal)
{
    return cal->timezones;
}

static void
AddTimezone(BongoCalObject *cal, GArray *components, BongoCalTime t)
{
    if (t.tz && t.tz->type == BONGO_CAL_TIMEZONE_SYSTEM) {
        BongoCalTimezone *newTz;
        
        newTz = BongoCalTimezoneNewJsonForSystem(cal, t.tz->tzid);
        if (newTz) {
            BongoJsonArrayAppendObject(components, newTz->json);
            BongoHashtablePut(cal->timezones, newTz->tzid, newTz);
        }
    }
}

static void
AddTimezones(BongoCalObject *cal, GArray *components, BongoCalInstance *inst)
{
    AddTimezone(cal, components, BongoCalInstanceGetStart(inst));
    AddTimezone(cal, components, BongoCalInstanceGetEnd(inst));
}

/* Include a copy of system timezones in the json */
void 
BongoCalObjectResolveSystemTimezones(BongoCalObject *cal)
{
    unsigned int i;
    GArray *components;

    if (BongoJsonObjectGetArray(cal->json, "components", &components) != BONGO_JSON_OK) {
        return;
    }
    
    for (i = 0; i < cal->instances->len; i++) {
        BongoCalInstance *inst = g_array_index(cal->instances, BongoCalInstance *, i);
        
        AddTimezones(cal, components, inst);
    }
}

void
BongoCalObjectStripSystemTimezones(BongoCalObject *cal)
{
    BongoHashtableIter iter;
    BongoSList *remove = NULL;
    BongoSList *l;
    GArray *components;

    if (BongoJsonObjectGetArray(cal->json, "components", &components) != BONGO_JSON_OK) {
        return;
    }

    for (BongoHashtableIterFirst(cal->timezones, &iter);
         iter.key != NULL;
         BongoHashtableIterNext(cal->timezones, &iter)) {
        BongoCalTimezone *tz;
        
        tz = (BongoCalTimezone*)iter.value;
        if (tz->type == BONGO_CAL_TIMEZONE_JSON) {
            if (!strncmp(tz->tzid, "/bongo/", strlen("/bongo/")) && 
                BongoCalObjectGetTimezone(cal, tz->tzid) != NULL) {
                remove = BongoSListAppend(remove, tz);
            }
        }
    }

    for (l = remove; l != NULL; l = l->next) {
        BongoCalTimezone *tz;
        unsigned int i;
        
        tz = l->data;
        for (i = 0; i < components->len; i++) {
            BongoJsonObject *obj;
            
            if (BongoJsonArrayGetObject(components, i, &obj) == BONGO_JSON_OK) {
                BongoJsonArrayRemove(components, i);
                break;
            }
        }

        BongoHashtableRemove(cal->timezones, tz->tzid);
    }

    BongoSListFree(remove);
}

BongoCalInstance *
BongoCalObjectGetInstance(BongoCalObject *cal, const char *uid, BongoCalTime recurid)
{
    int i;
    IdSearchData data;
    
    if (!cal->instancesSorted) {
        g_array_sort(cal->instances, CompareInstancesId);
        cal->instancesSorted = TRUE;
    }

    data.uid = uid;
    data.recurid = recurid;

    i = GArrayFindSorted(cal->instances, &data, FindInstanceById);    
    if (i != -1) {
        return g_array_index(cal->instances, BongoCalInstance *, i);
    } else {
        return FALSE;
    }
}

static BOOL
ExpandCb(BongoCalInstance *inst,
         uint64_t instanceStart,
         uint64_t instanceEnd,
         void *datap)
{
    BongoCalTime originalStart;
    BongoCalTime originalEnd;
    BongoCalOccurrence occ;
    CollectData *data = datap;


    originalStart = BongoCalInstanceGetStart(inst);
    occ.start = BongoCalTimeNewFromUint64(instanceStart, 
                                         originalStart.isDate, 
                                         originalStart.tz);

    originalEnd = BongoCalInstanceGetEnd(inst);    
    occ.end = BongoCalTimeNewFromUint64(instanceEnd, 
                                       originalEnd.isDate, 
                                       originalEnd.tz);

    if (BongoCalObjectGetInstance(data->cal, 
                                 BongoCalInstanceGetUid(inst),
                                 occ.start)) {
        /* Include the exceptional instance instead */
        return TRUE;
    }
    
    occ.recurid = occ.start;
    occ.generated = TRUE;
    occ.instance = inst;

    g_array_append_val(data->occs, occ);

    return TRUE;
}

static BongoCalTimezone *
TimezoneCb(const char *tzid, void *data)
{
    BongoCalObject *cal = data;
    
    return BongoCalObjectGetTimezone(cal, tzid);
}

BongoCalOccurrence
BongoCalObjectPrimaryOccurrence(BongoCalObject *cal,
                               BongoCalTimezone *defaultTz)
{
    UNUSED_PARAMETER_REFACTOR(defaultTz)
    if (cal->instances->len > 0) {
        /* This needs to have some logic to find the primary instance,
           rather than the first instance.  However, for now we're
           just using this in CalCmd, and we don't create events with
           multiple occurrances there. */
        BongoCalInstance *instance;

        instance = g_array_index(cal->instances, BongoCalInstance *, 0);

        return BongoCalInstanceGetOccurrence(instance);
    } else {
        BongoCalOccurrence occ;

        occ.start = BongoCalTimeEmpty();
        occ.end = BongoCalTimeEmpty();

        return occ;
    }
}

BOOL
BongoCalObjectCollect(BongoCalObject *cal,
                     BongoCalTime start,
                     BongoCalTime end,
                     BongoCalTimezone *defaultTz,
                     BOOL includeGenerated,
                     GArray *occurrences)
{
    unsigned int i;
    unsigned int lenBefore;
    
    lenBefore = occurrences->len;

    for (i = 0; i < cal->instances->len; i++) {
        BOOL add;
        BongoCalInstance *instance;
        CollectData data;

        instance = g_array_index(cal->instances, BongoCalInstance *, i);

        add = TRUE;

        /* Don't add the main instances if there's an override for it, or if generating instances (the generator will include the main instance) */
        if (!BongoCalInstanceIsRecurrence(instance)) {
            if (includeGenerated) {
                add = FALSE;
                
                data.cal = cal;
                data.occs = occurrences;
            
                BongoCalRecurGenerateInstances(instance, 
                                              BongoCalTimeAsUint64(start), 
                                              BongoCalTimeAsUint64(end), 
                                              ExpandCb, &data, 
                                              TimezoneCb, cal,
                                              defaultTz);
            } else if (BongoCalObjectGetInstance(cal, BongoCalInstanceGetUid(instance), BongoCalInstanceGetStart(instance))) {
                add = FALSE;
            }
        }
        
        /* Add the instance if it wasn't generated by the generator */
        if (add && BongoCalInstanceCrosses(instance, start, end)) {
            BongoCalOccurrence occ;
                
            occ = BongoCalInstanceGetOccurrence(instance);
            
            g_array_append_val(occurrences, occ);
        }
    }

    return (occurrences->len > lenBefore);
}

GArray *
BongoCalObjectGetInstances(BongoCalObject *cal) 
{
    return cal->instances;
}

BOOL
BongoCalObjectIsSingle(BongoCalObject *cal)
{
    /* FIXME: wrong */
    return (cal->instances->len < 2);
}

BongoCalInstance *
BongoCalObjectGetSingleInstance(BongoCalObject *cal)
{
    /* FIXME: wrong */
    if (cal->instances->len == 1) {
        return g_array_index(cal->instances, BongoCalInstance *, 0);
    } else {
        return NULL;
    }
}

#if 0
void
BongoCalObjectLocalizeTimes(BongoCalObject *cal,
                           BongoCalTimezone *tz,
                           const char *attribute)
{
    unsigned int i;
    
    for (i = 0; i < cal->instances->len; i++) {
        BongoCalInstance *inst = g_array_index(cal->instances, BongoCalInstance *, i);
        BongoCalInstanceLocalizeTimes(inst, tz, attribute);
    }
}
#endif

BOOL
BongoCalObjectAddInstance(BongoCalObject *cal,
                         BongoCalInstance *inst)
{
    GArray *components;
    
    if (BongoJsonObjectGetArray(cal->json, "components", &components) != BONGO_JSON_OK) {
        components = g_array_new(FALSE, FALSE, sizeof(BongoJsonNode *));
        if (BongoJsonObjectPutArray(cal->json, "components", components) != BONGO_JSON_OK) {
            return FALSE;
        }
    }

    inst->cal = cal;
    BongoJsonArrayAppendObject(components, inst->json);
    g_array_append_val(cal->instances, inst);

    cal->instancesSorted = FALSE;

    return TRUE;
}

BOOL
BongoCalInit(const char *cachedir)
{
    return BongoCalTimezoneInit(cachedir);
}
