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

/* Some code from libical: */

/*======================================================================
 FILE: icaltimezone.c
 CREATOR: Damon Chaplin 15 March 2001

 $Id: icaltimezone.c,v 1.22 2003/10/21 18:28:27 ettore Exp $
 $Locker:  $

 (C) COPYRIGHT 2001, Damon Chaplin

 This program is free software; you can redistribute it and/or modify
 it under the terms of either:

    The LGPL as published by the Free Software Foundation, version
    2.1, available at: http://www.fsf.org/copyleft/lesser.html

  Or:

    The Mozilla Public License Version 1.0. You may obtain a copy of
    the License at http://www.mozilla.org/MPL/


======================================================================*/


#include <config.h>
#include "bongo-cal-private.h"
#include <bongocal.h>
#include <ical-wrapper.h>

typedef struct {
    int utcOffset;
    int prevUtcOffset;
    
    int year;
    int month;
    int day;
    int hour;
    int minute;
    int second;
    int isDaylight;
} TimezoneChange;

BongoCalTimezone utcTz = { BONGO_CAL_TIMEZONE_OFFSET, "UTC", 0, NULL, NULL, NULL, 0, 0 };
static int timezonesInitialized = FALSE;
static BongoHashtable *systemTimezones = NULL;

static void ReadChanges(BongoCalTimezone *tz);

BongoCalTimezone *
BongoCalTimezoneNewJson(BongoCalObject *cal, BongoJsonObject *json, const char *tzid)
{
    BongoCalTimezone *tz;

    if (tzid == NULL) {
        BongoJsonObjectResolveString(json, "tzid/value", &tzid);
    }

    tz = MemMalloc0(sizeof(BongoCalTimezone));
    tz->type = BONGO_CAL_TIMEZONE_JSON;
    tz->json = json;
    tz->cal = cal;
    if (tzid != NULL)
        tz->tzid = MemStrdup(tzid);
    else
        tz->tzid = NULL;

    ReadChanges(tz);
    
    return tz;
}

BongoCalTimezone *
BongoCalTimezoneNewJsonForSystem(BongoCalObject *cal, const char *tzid)
{
    char path[PATH_MAX];
    char *p;
    FILE *tzfile;
    BongoJsonNode *tzNode;
    BongoJsonObject *tzObj;
    GArray *components;

    if (!tzid || strncmp(tzid, "/bongo/", strlen("/bongo/")) != 0) {
        return NULL;
    }
    
    snprintf(path, PATH_MAX, "%s/%s.zone", ZONEINFODIR, tzid + strlen("/bongo/"));
    for (p = path + strlen(ZONEINFODIR) + 1; *p != '\0'; p++) {
        if (*p == '/') {
            *p = '-';
        }
    }

    tzfile = fopen(path, "r");
    if (!tzfile) {
        return NULL;
    }

    if (BongoJsonParseFile(tzfile, -1, &tzNode) != BONGO_JSON_OK) {
        fclose(tzfile);
        return NULL;
    }

    fclose(tzfile);

    /* Find the actual timezone definition */
    if (BongoJsonNodeResolveArray(tzNode, "components", &components) != BONGO_JSON_OK) {
        BongoJsonNodeFree(tzNode);
        return NULL;
    }

    if (BongoJsonArrayGetObject(components, 0, &tzObj) != BONGO_JSON_OK) {
        BongoJsonNodeFree(tzNode);
        return NULL;
    }
    
    /* Throw away the rest */
    BongoJsonArrayRemoveSteal(components, 0);
    BongoJsonNodeFree(tzNode);

    return BongoCalTimezoneNewJson(cal, tzObj, tzid);
}

BongoCalTimezone *
BongoCalTimezoneGetUtc(void)
{
    return &utcTz;
}

BongoCalTimezone *
BongoCalTimezoneGetSystem(const char *tzid)
{
    if (!tzid) {
        return &utcTz;
    }
    
    if (strncmp(tzid, "/bongo/", strlen("/bongo/")) != 0) {
        return NULL;
    }

    assert(systemTimezones);

    return BongoHashtableGet(systemTimezones, tzid);
}

void
BongoCalTimezoneFree(BongoCalTimezone *tz, BOOL freeJson)
{
	if (tz == &utcTz) return;

	if (tz->tzid)
		MemFree(tz->tzid);

	if (tz->changes) {
		g_array_free(tz->changes, FALSE);
	}

	if (freeJson)
		BongoJsonObjectFree(tz->json);

	MemFree(tz);
}

BongoCalTime 
BongoCalTimezoneConvertTime(BongoCalTime t,
                           BongoCalTimezone *to)
{
    int utcOffset;
    int isDaylight;
    BongoCalTimezone *from;

    if (t.isUtc) {
        from = &utcTz;
    } else {
        from = t.tz;
    }

    if (to == NULL) {
        to = &utcTz;
    }

    /* If there's no time or no conversion, or the time is a floating time, just return */ 
    if (t.isDate || from == to || (!t.isUtc && t.tz == NULL)) {
        return t;
    }

    /* Convert the time to UTC */
    utcOffset = BongoCalTimezoneGetUtcOffset(from, t, FALSE, NULL);

    t = BongoCalTimeAdjust(t, 0, 0, 0, -utcOffset);
    
    /* Convert from UTC to the new timezone */
    utcOffset = BongoCalTimezoneGetUtcOffset(to, t, TRUE, &isDaylight);

    if (to != &utcTz) {
        t.isUtc = FALSE;
    }

    t = BongoCalTimeSetTimezone(t, to);

    return BongoCalTimeAdjust(t, 0, 0, 0, utcOffset);
}

static TimezoneChange
AdjustChange(TimezoneChange change, int days, int hours, int minutes, int seconds)
{
    BongoCalTime t;
    
    t = BongoCalTimeEmpty();

    t.second = change.second;
    t.minute = change.minute;
    t.hour = change.hour;
    t.day = change.day;
    t.month = change.month;
    t.year = change.year;

    BongoCalTimeAdjust(t, days, hours, minutes, seconds);

    change.second = t.second;
    change.minute = t.minute;
    change.hour = t.hour;
    change.day = t.day;
    change.month = t.month;
    change.year = t.year;

    return change;
}

static int
CompareChange (const void     *elem1,
               const void     *elem2)
{
    const TimezoneChange *change1, *change2;
    int retval;

    change1 = elem1;
    change2 = elem2;

    if (change1->year < change2->year)
        retval = -1;
    else if (change1->year > change2->year)
        retval = 1;
    else if (change1->month < change2->month)
        retval = -1;
    else if (change1->month > change2->month)
        retval = 1;
    else if (change1->day < change2->day)
        retval = -1;
    else if (change1->day > change2->day)
        retval = 1;
    else if (change1->hour < change2->hour)
        retval = -1;
    else if (change1->hour > change2->hour)
        retval = 1;
    else if (change1->minute < change2->minute)
        retval = -1;
    else if (change1->minute > change2->minute)
        retval = 1;
    else if (change1->second < change2->second)
        retval = -1;
    else if (change1->second > change2->second)
        retval = 1;
    else
        retval = 0;

    return retval;
}

static void
AddOccurrence(BongoCalTimezone *tz, 
              BongoCalOccurrence occ,
              BOOL updateBounds)
{
    TimezoneChange change;
    BongoCalType type;
    
    type = BongoCalInstanceGetType(occ.instance);
    if (type == BONGO_CAL_TYPE_UNKNOWN) {
        return;
    }

    if (type == BONGO_CAL_TYPE_TZSTANDARD) {
        change.isDaylight = FALSE;
    } else if (type == BONGO_CAL_TYPE_TZDAYLIGHT) {
        change.isDaylight = TRUE;
    } else {
        return;
    }

    if (BongoCalTimeIsEmpty(occ.start)) {
        return;
    }

    change.utcOffset = BongoCalInstanceGetTzOffsetTo(occ.instance);
    change.prevUtcOffset = BongoCalInstanceGetTzOffsetFrom(occ.instance);

    change.year = occ.start.year;
    change.month = occ.start.month;
    change.day = occ.start.day;
    change.hour = occ.start.hour;
    change.minute = occ.start.minute;
    change.second = occ.start.second;
    
    change = AdjustChange(change, 0, 0, 0, change.prevUtcOffset);
    g_array_append_val(tz->changes, change);

    if (updateBounds) {
        uint64_t startTime = BongoCalTimeAsUint64(occ.start);
        if (tz->changesStart == 0 || startTime < tz->changesStart) {
            tz->changesStart = startTime;
        }

        if (tz->changesEnd == 0 || startTime > tz->changesEnd) {
            tz->changesEnd = startTime;
        }
    }
}

static void
ReadChanges(BongoCalTimezone *tz)
{
    GArray *changes;
    
    if (tz->type != BONGO_CAL_TIMEZONE_JSON) {
        return;
    }

    if (tz->changes) {
        g_array_free(tz->changes, FALSE);
    }
    
    tz->changes = g_array_sized_new(FALSE, FALSE, sizeof(TimezoneChange), 32);

    if (BongoJsonObjectGetArray(tz->json, "changes", &changes) == BONGO_JSON_OK) {
        unsigned int i;
        for (i = 0; i < changes->len; i++) {
            BongoJsonObject *jsonInst = NULL;
            if (BongoJsonArrayGetObject(changes, i, &jsonInst) == BONGO_JSON_OK) {
                BongoCalInstance calInst;
                
                memset(&calInst, 0, sizeof(BongoCalInstance));
                BongoCalInstanceInit(&calInst, tz->cal, jsonInst);
   
                AddOccurrence(tz, BongoCalInstanceGetOccurrence(&calInst), TRUE);
                
                BongoCalInstanceClear(&calInst, FALSE);
            }
        }
    }

    g_array_sort(tz->changes, CompareChange);
}

static BOOL
ExpandCb(BongoCalInstance *inst,
         uint64_t instanceStart,
         uint64_t instanceEnd,
         void *datap)
{
    BongoCalOccurrence occ;
    BongoCalTimezone *tz = datap;
    
    UNUSED_PARAMETER_REFACTOR(instanceEnd)

    occ.start = BongoCalTimeNewFromUint64(instanceStart, FALSE, BongoCalTimezoneGetUtc());
    occ.end = BongoCalTimeNewFromUint64(instanceStart, FALSE, BongoCalTimezoneGetUtc());
    occ.recurid = occ.start;
    occ.generated = TRUE;
    occ.instance = inst;
    
    AddOccurrence(tz, occ, FALSE);

    return TRUE;
}


static BongoCalTimezone *
TimezoneCb(const char *tzid, void *data)
{
    BongoCalObject *cal = data;
    
    return BongoCalObjectGetTimezone(cal, tzid);
}

void
BongoCalTimezoneExpand(BongoCalTimezone *tz, BongoCalTime start, BongoCalTime end)
{                
    GArray *changes;
    uint64_t startt;
    uint64_t endt;
    
    startt = BongoCalTimeAsUint64(start);
    endt = BongoCalTimeAsUint64(end);
    
    if (tz->type != BONGO_CAL_TIMEZONE_JSON) {
        return;
    }

    if (tz->changesStart <= startt && tz->changesEnd >= endt) {
        return;
    }
    
    if (tz->changes) {
        g_array_free(tz->changes, FALSE);
    }
    
    tz->changes = g_array_sized_new(FALSE, FALSE, sizeof(TimezoneChange), 32);
    
    if (BongoJsonObjectGetArray(tz->json, "changes", &changes) == BONGO_JSON_OK) {
        unsigned int i;

        for (i = 0; i < changes->len; i++) {
            BongoJsonObject *jsonInst = NULL;
            if (BongoJsonArrayGetObject(changes, i, &jsonInst) == BONGO_JSON_OK) {
                BongoCalInstance calInst;
                
                memset(&calInst, 0, sizeof(BongoCalInstance));
                BongoCalInstanceInit(&calInst, tz->cal, jsonInst);

                /* Add the original instances */
                AddOccurrence(tz, BongoCalInstanceGetOccurrence(&calInst), FALSE);

                /* Add generated instances */
                BongoCalRecurGenerateInstances(&calInst,
                                              startt,
                                              endt,
                                              ExpandCb, tz,
                                              TimezoneCb, tz->cal,
                                              BongoCalTimezoneGetUtc());                              

                
                BongoCalInstanceClear(&calInst, FALSE);
            }
        }
    }

    g_array_sort(tz->changes, CompareChange);

    tz->changesStart = startt;
    tz->changesEnd = endt;
}

static void
EnsureCoverage(BongoCalTimezone *tz, BongoCalTime time)
{
    if (tz->type == BONGO_CAL_TIMEZONE_JSON) {
        BongoCalTime start;
        BongoCalTime end;

        start = BongoCalTimeNewDate(0, 1, time.year);
        end = BongoCalTimeNewDate(0, 1, time.year + 1);

        BongoCalTimezoneExpand(tz, start, end);
    }
}

static int
FindNearbyChange(BongoCalTimezone *tz, TimezoneChange *change)
{
    TimezoneChange *zoneChange;
    
    int lower, upper, middle, cmp;
    
    lower = middle = 0;
    upper = tz->changes->len;

    while (lower < upper) {
        middle = (lower + upper) / 2;

        zoneChange = &g_array_index(tz->changes, TimezoneChange, middle);

        cmp = CompareChange(change, zoneChange);

        if (cmp == 0) {
            break;
        } else if (cmp < 0) {
            upper = middle;
        } else {
            lower = middle + 1;
        }
    }

    return middle;
}

int
BongoCalTimezoneGetUtcOffset(BongoCalTimezone *tz,
                            BongoCalTime t,
                            BOOL timeAsUtc,
                            BOOL *isDaylight)
{
    TimezoneChange tChange;
    TimezoneChange *zoneChange;
    TimezoneChange *prevZoneChange;
    int changeNum;
    int changeNumToUse;
    int step;
    int utcOffsetChange;
    int cmp;
    
    if (isDaylight) {
        *isDaylight = 0;
    }
    
    if (tz->type == BONGO_CAL_TIMEZONE_OFFSET) {
        return tz->offset;
    }

    EnsureCoverage(tz, t);
    
    if (!tz->changes || tz->changes->len == 0) {
        return 0;
    }
    
    tChange.year = t.year;
    tChange.month = t.month;
    tChange.day = t.day;
    tChange.hour = t.hour;
    tChange.minute = t.minute;
    tChange.second = t.second;
    
    changeNum = FindNearbyChange(tz, &tChange);

    assert(changeNum >= 0);
    assert((unsigned int)changeNum < tz->changes->len);
    
    /* Now move backwards or forwards to find the tz change that applies */
    zoneChange = &g_array_index(tz->changes, TimezoneChange, changeNum);

    step = 1;
    changeNumToUse = -1;
    
    while (1) {
        TimezoneChange tmpChange;
        
        /* Copy the change so we can adjust it */
        tmpChange = *zoneChange;
        
        if (timeAsUtc) {
            /* If the clock is going backward, check if it is in the region of time
               that is used twice. If it is, use the change with the daylight
               setting which matches tt, or use standard if we don't know. */
            if (tmpChange.utcOffset < tmpChange.prevUtcOffset) {
                /* If the time change is at 2:00AM local time and the clock is
                   going back to 1:00AM we adjust the change to 1:00AM. We may
                   have the wrong change but we'll figure that out later. */
                tmpChange = AdjustChange(tmpChange, 0, 0, 0, tmpChange.utcOffset);
            } else {
                tmpChange = AdjustChange(tmpChange, 0, 0, 0, tmpChange.utcOffset);
            }
        }
        
        cmp = CompareChange (&tChange, &tmpChange);

        /* If the given time is on or after this change, then this change may
           apply, but we continue as a later change may be the right one.
           If the given time is before this change, then if we have already
           found a change which applies we can use that, else we need to step
           backwards. */
        if (cmp >= 0) {
            changeNumToUse = changeNum;
        } else {
            step = -1;
        }

        /* If we are stepping backwards through the changes and we have found
           a change that applies, then we know this is the change to use so
           we exit the loop. */
        if (step == -1 && changeNumToUse != -1) {
            break;
        }
        
        changeNum += step;
        
        /* If we go past the start of the changes array, then we have no data
           for this time so we return a UTC offset of 0. */
        if (changeNum < 0) {
            return 0;
        }
        

        if ((unsigned int)changeNum >= tz->changes->len) {
            break;
        }
        
        zoneChange = &g_array_index(tz->changes, TimezoneChange, changeNum);
    }
        
    /* If we didn't find a change to use, then we have a bug! */
    assert (changeNumToUse != -1);

    /* Now we just need to check if the time is in the overlapped region of
       time when clocks go back. */
    zoneChange = &g_array_index(tz->changes, TimezoneChange, changeNumToUse);

    utcOffsetChange = zoneChange->utcOffset - zoneChange->prevUtcOffset;
    if (utcOffsetChange < 0 && changeNumToUse > 0) {
        TimezoneChange tmpChange = *zoneChange;

        tmpChange = AdjustChange (tmpChange, 0, 0, 0, tmpChange.prevUtcOffset);
        if (CompareChange (&tChange, &tmpChange) < 0) {
            /* The time is in the overlapped region, so we may need to use
               either the current zone_change or the previous one. If the
               time has the is_daylight field set we use the matching change,
               else we use the change with standard time. */
            prevZoneChange = &g_array_index(tz->changes, TimezoneChange, changeNumToUse - 1);
        }
    }

    /* Now we know exactly which timezone change applies to the time, so
       we can return the UTC offset and whether it is a daylight time. */
    if (isDaylight)
        *isDaylight = zoneChange->isDaylight;
    return zoneChange->utcOffset;
}

const char * 
BongoCalTimezoneGetTzid(BongoCalTimezone *tz)
{
    if (!tz || tz == &utcTz) {
        return "UTC";
    }
    
    return tz->tzid;
}

static BOOL
PackString(FILE *to, char *str)
{
    unsigned int len;
    len = strlen(str);

    if (fwrite(&len, sizeof(unsigned int), 1, to) != 1) {
        return FALSE;
    }

    if (fwrite(str, 1, len, to) != len) {
        return FALSE;
    }   

    return TRUE;
}

static BOOL 
UnpackString(FILE *from, char **str)
{
    unsigned int len;
    
    if (fread(&len, sizeof(unsigned int), 1, from) != 1) {
        return FALSE;
    }

    *str = MemMalloc(len + 1);
    
    if (fread(*str, 1, len, from) != len) {
        MemFree(*str);
        *str = NULL;
        return FALSE;
    }
    (*str)[len] = '\0';

    return TRUE;
}

static BOOL
PackArray(FILE *to, GArray *array, unsigned int elemSize)
{
    if (fwrite(&array->len, sizeof(unsigned int), 1, to) != 1) {
        return FALSE;
    }
    if (fwrite(&elemSize, sizeof(unsigned int), 1, to) != 1) {
        return FALSE;
    }
    
    if (fwrite(array->data, elemSize, array->len, to) != array->len) {
        return FALSE;
    }

    return TRUE;
}

static BOOL
UnpackArray(FILE *from, GArray **array, unsigned int expectedElemSize)
{
    unsigned int len;
    unsigned int elemSize;
    void *data;
    
    if (fread(&len, sizeof(unsigned int), 1, from) != 1) {
        return FALSE;
    }

    if (fread(&elemSize, sizeof(unsigned int), 1, from) != 1) {
        return FALSE;
    }

    if  (elemSize != expectedElemSize) {
        return FALSE;
    }
    
    data = MemMalloc(len * elemSize);
    
    if (fread(data, elemSize, len, from) != len) {
        MemFree(data);
        return FALSE;
    }
    
    *array = g_array_new(FALSE, FALSE, sizeof(char *));
    g_array_append_val(*array, data);
    
    return TRUE;
}

#define PACK_MAGIC 123

BOOL
BongoCalTimezonePack(BongoCalTimezone *tz, FILE *to)
{
    /* Magic number */
    if (fputc(PACK_MAGIC, to) == EOF) {
        return FALSE;
    }
    
    if (!PackString(to, tz->tzid)) {
        return FALSE;
    }

    if (fwrite(&tz->changesStart, sizeof(uint64_t), 1, to) != 1) {
        return FALSE;
    }

    if (fwrite(&tz->changesEnd, sizeof(uint64_t), 1, to) != 1) {
        return FALSE;
    }

    if (!PackArray(to, tz->changes, sizeof(TimezoneChange))) {
        return FALSE;
    }

    return TRUE;
}

BongoCalTimezone *
BongoCalTimezoneUnpack(FILE *from)
{
    BongoCalTimezone *tz;
    char *tzid;
    uint64_t changesStart;
    uint64_t changesEnd;
    GArray *changes;
    
    if (fgetc(from) != PACK_MAGIC) {
        return NULL;
    }

    if (!UnpackString(from, &tzid)) {
        return NULL;
    }

    if (fread(&changesStart, sizeof(uint64_t), 1, from) != 1) {
        MemFree(tzid);
        return NULL;
    }

    if (fread(&changesEnd, sizeof(uint64_t), 1, from) != 1) {
        MemFree(tzid);
        return NULL;
    }

    if (!UnpackArray(from, &changes, sizeof(TimezoneChange))) {
        fflush(stdout);
        MemFree(tzid);
        return NULL;
    }

    tz = MemMalloc0(sizeof(BongoCalTimezone));
    tz->type = BONGO_CAL_TIMEZONE_SYSTEM;
    tz->tzid = tzid;
    tz->changesStart = changesStart;
    tz->changesEnd = changesEnd;
    tz->changes = changes;

    return tz;
}

static void
FreeTimezone(void *value)
{
    BongoCalTimezoneFree(value, FALSE);
}

static BOOL
LoadSystemTimezones(FILE *f)
{
    BongoCalTimezone *tz;
    BOOL badCache;
    
    systemTimezones = BongoHashtableCreateFull(500, 
                                              (HashFunction)BongoStringHash,
                                              (CompareFunction)strcmp,
                                              NULL,
                                              FreeTimezone);

    badCache = FALSE;
    do {
        tz = BongoCalTimezoneUnpack(f);
        if (!tz && !feof(f)) {
            badCache = TRUE;
            break;
        } 
        if (tz) {
            BongoHashtablePut(systemTimezones, tz->tzid, tz);
        }
    } while (tz);

    if (badCache) {
        printf("libbongocal: bad timezone cache\n");
        BongoHashtableDelete(systemTimezones);
        systemTimezones = NULL;
        return FALSE;
    }

    return TRUE;
}

static void
UnloadSystemTimezones() {
    BongoHashtableDelete(systemTimezones);
}

static BOOL
LoadCachedTimezones(const char *cachedir)
{
    char path[PATH_MAX];
    FILE *f;
    
    snprintf(path, PATH_MAX, "%s/%s", cachedir, "bongo-tz.cache");
     
    f = fopen(path, "rb");
    
    if (f) {
        BOOL success;
        success = LoadSystemTimezones(f);
        fclose(f);
        
        return success;
    } else {
        return FALSE;
    }
}

static void
UnloadCachedTimezones() {
    UnloadSystemTimezones();
}

static BOOL
CacheSystemTimezone(FILE *tzfile, FILE *cachefile, BongoCalTime expandFrom, BongoCalTime expandUntil)
{
    BongoJsonNode *tzNode;
    BongoJsonObject *tzObj;
    BongoCalObject *cal;
    BongoHashtable *tzs;
    BOOL ret;

    if (BongoJsonParseFile(tzfile, -1, &tzNode) != BONGO_JSON_OK) {
        return FALSE;
    }

    if (tzNode->type != BONGO_JSON_OBJECT) {
        BongoJsonNodeFree(tzNode);
        return FALSE;
    }
    
    tzObj = BongoJsonNodeAsObject(tzNode);

    cal = BongoCalObjectNew(tzObj);
    tzs = BongoCalObjectGetTimezones(cal);

    ret = TRUE;
    if (tzs) {
        BongoHashtableIter iter;
        for (BongoHashtableIterFirst(tzs, &iter);
             iter.key != NULL;
             BongoHashtableIterNext(tzs, &iter)) {
            BongoCalTimezone *tz;
            
            tz = iter.value;

            BongoCalTimezoneExpand(tz, expandFrom, expandUntil);
            if (!BongoCalTimezonePack(tz, cachefile)) {
                ret = FALSE;
                break;
                
            }
        }
    }
    BongoCalObjectFree(cal, FALSE);
    BongoJsonNodeFree(tzNode);
    
    return ret;
}

static BOOL
CacheSystemTimezones(const char *cachedir)
{
    struct dirent *ent;
    DIR *dir;
    FILE *f;
    char filename[PATH_MAX];
    BongoCalTime until;
    BongoCalTime from;
    BOOL ret;

    dir = opendir(ZONEINFODIR);
    if (!dir) {
        printf("libbongocal: Couldn't find zoneinfo in %s\n", ZONEINFODIR);
    }

    f = XplOpenTemp(cachedir, "wb", filename);
    if (!f) {
        closedir(dir);
        printf("libbongocal: Couldn't open temporary file %s\n", filename);
        return FALSE;
    }

    from = BongoCalTimeNewFromUint64(0, FALSE, NULL);
    until = BongoCalTimeNewDate(1, 0, 2099);

    ret = TRUE;
    while ((ent = readdir(dir)) != NULL) {
        char path[PATH_MAX];
        FILE *tzfile;
        int len;
        
        len = strlen(ent->d_name);
        if (len < 5 || strcmp(ent->d_name + (len - 5), ".zone") != 0) {
            continue;
        }
        
        snprintf(path, PATH_MAX, "%s/%s", ZONEINFODIR, ent->d_name);

        tzfile = fopen(path, "r");

        if (!CacheSystemTimezone(tzfile, f, from, until)) {
            printf("libbongocal: Couldn't cache timezones in %s\n", path);
            ret = FALSE;
            break;
        }
        
        fclose(tzfile);
    }

    fclose(f);

    if (ret) {
        char cachename[PATH_MAX];
        snprintf(cachename, PATH_MAX, "%s/%s", cachedir, "bongo-tz.cache");
        if (rename(filename, cachename) != 0) {
            ret = FALSE;
        }
        if (chmod(cachename, 0644) != 0) {
            ret = FALSE;
        }
    } else {
        unlink(filename);
    }

    return ret;
}

BOOL
BongoCalTimezoneInit(const char *cachedir)
{
    if (timezonesInitialized) {
        return TRUE;
    }

    if (LoadCachedTimezones(cachedir)) {
        return TRUE;
    }

    if (!CacheSystemTimezones(cachedir)) {
        printf("libbongocal: Couldn't generate system timezone cache\n");
        return FALSE;
    }
    
    timezonesInitialized = TRUE;

    return LoadCachedTimezones(cachedir);
}

void
BongoCalTimezoneDeInit() {
    UnloadCachedTimezones();
}
