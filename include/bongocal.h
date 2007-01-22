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

#ifndef BONGOCAL_H
#define BONGOCAL_H

#include <xpl.h>
#include <libical/ical.h>
#include <bongojson.h>

XPL_BEGIN_C_LINKAGE

#define BONGO_CAL_BONGO_PRODID "-//Bongo Project//Bongo 1.0//EN"

#define BONGO_CAL_TIME_BUFSIZE 30

typedef struct _BongoCalObject BongoCalObject;
typedef struct _BongoCalInstance BongoCalInstance;
typedef struct _BongoCalTimezone BongoCalTimezone;

typedef struct {
    int year;              /* full year, ie 2001 */
    int month;             /* 0 - 11 */
    int day;               /* 1 - 31 */
    int hour;              /* 0 - 23 */
    int minute;            /* 0 - 59 */ 
    int second;            /* 0 - 59 */
    int isDate;

    BOOL isUtc;
    const char *tzid;
    BongoCalTimezone *tz;
} BongoCalTime;

typedef struct {
    int isNeg;
    unsigned int weeks;
    unsigned int days;
    unsigned int hours;
    unsigned int minutes;
    unsigned int seconds;
} BongoCalDuration;

typedef enum {
    BONGO_CAL_PERIOD_DATETIME,
    BONGO_CAL_PERIOD_DURATION,
} BongoCalPeriodType;

typedef struct {
    BongoCalPeriodType type;
    
    BongoCalTime start;
    
    union {
        BongoCalTime end;
        BongoCalDuration duration;
    } value;
} BongoCalPeriod;

typedef enum {
    BONGO_CAL_RDATE_DATETIME,
    BONGO_CAL_RDATE_PERIOD,
} BongoCalRDateType;

typedef struct {
    BongoCalRDateType type;

    union {
        BongoCalTime time;
        BongoCalPeriod period;
    } value;
} BongoCalRDate;

typedef enum {
    BONGO_CAL_RECUR_NONE,
    BONGO_CAL_RECUR_SECONDLY,
    BONGO_CAL_RECUR_MINUTELY,
    BONGO_CAL_RECUR_HOURLY,
    BONGO_CAL_RECUR_DAILY,
    BONGO_CAL_RECUR_WEEKLY,
    BONGO_CAL_RECUR_MONTHLY,
    BONGO_CAL_RECUR_YEARLY,
} BongoCalRecurFrequency;

typedef struct {
    BongoCalRecurFrequency freq;

    int interval;

    /* Specifies the end of the recurrence, inclusive. No occurrences are
       generated after this date. If it is 0, the event recurs forever. */
    int count;
    uint64_t until;

    /* WKST property - the week start day: 0 = Monday to 6 = Sunday. */
    int weekStartDay;

    /* NOTE: I've used GList's here, but it doesn't matter if we use
       other data structures like arrays. The code should be easy to
       change. So long as it is easy to see if the modifier is set. */

    /* For BYMONTH modifier. A list of XPL_INT_TO_PTRs, 0-11. */
    BongoList              *bymonth;

    /* For BYWEEKNO modifier. A list of XPL_INT_TO_PTRs, [+-]1-53. */
    BongoList              *byweekno;

    /* For BYYEARDAY modifier. A list of XPL_INT_TO_PTRs, [+-]1-366. */
    BongoList              *byyearday;

    /* For BYMONTHDAY modifier. A list of XPL_INT_TO_PTRs, [+-]1-31. */
    BongoList              *bymonthday;

    /* For BYDAY modifier. A list of XPL_INT_TO_PTRs, in pairs.
       The first of each pair is the weekday, 0 = Monday to 6 = Sunday.
       The second of each pair is the week number [+-]0-53. */
    BongoList              *byday;

    /* For BYHOUR modifier. A list of XPL_INT_TO_PTRs, 0-23. */
    BongoList              *byhour;

    /* For BYMINUTE modifier. A list of XPL_INT_TO_PTRs, 0-59. */
    BongoList              *byminute;

    /* For BYSECOND modifier. A list of XPL_INT_TO_PTRs, 0-60. */
    BongoList              *bysecond;

    /* For BYSETPOS modifier. A list of XPL_INT_TO_PTRs, +ve or -ve. */
    BongoList              *bysetpos;

    /* The json object this recurrence rule was read from, if any */
    BongoJsonObject *json;
} BongoCalRule;

typedef struct {
    BongoCalTime recurid;
    BongoCalTime start;
    BongoCalTime end;
    BOOL generated;
    BongoCalInstance *instance;
} BongoCalOccurrence;

typedef enum {
    BONGO_CAL_TYPE_UNKNOWN,
    BONGO_CAL_TYPE_CALENDAR,
    BONGO_CAL_TYPE_EVENT,
    BONGO_CAL_TYPE_TODO,
    BONGO_CAL_TYPE_JOURNAL,
    BONGO_CAL_TYPE_FREEBUSY,
    BONGO_CAL_TYPE_ALARM,
    BONGO_CAL_TYPE_AUDIOALARM,
    BONGO_CAL_TYPE_DISPLAYALARM,
    BONGO_CAL_TYPE_EMAILALARM,
    BONGO_CAL_TYPE_PROCEDUREALARM,
    BONGO_CAL_TYPE_TIMEZONE,
    BONGO_CAL_TYPE_TZSTANDARD,
    BONGO_CAL_TYPE_TZDAYLIGHT,
} BongoCalType;

typedef enum {
    BONGO_CAL_METHOD_NONE,
    BONGO_CAL_METHOD_UNKNOWN,
    BONGO_CAL_METHOD_PUBLISH,
    BONGO_CAL_METHOD_REQUEST,
    BONGO_CAL_METHOD_REPLY,
    BONGO_CAL_METHOD_ADD,
    BONGO_CAL_METHOD_CANCEL,
    BONGO_CAL_METHOD_REFRESH,
    BONGO_CAL_METHOD_COUNTER,
    BONGO_CAL_METHOD_DECLINECOUNTER,
    BONGO_CAL_METHOD_CREATE,
    BONGO_CAL_METHOD_READ,
    BONGO_CAL_METHOD_RESPONSE,
    BONGO_CAL_METHOD_MOVE,
    BONGO_CAL_METHOD_MODIFY,
    BONGO_CAL_METHOD_GENERATEUID,
    BONGO_CAL_METHOD_DELETE,
} BongoCalMethod;
    
/*** BongoCal ***/

BOOL BongoCalInit(const char *cachedir);

/*** BongoCalObject ***/

BongoCalObject *BongoCalObjectNew(BongoJsonObject *json);

BongoCalObject *BongoCalObjectParseString(const char *str);
BongoCalObject *BongoCalObjectParseFile(FILE *f, int toRead);
BongoCalObject *BongoCalObjectParseIcalString(const char *str);

void BongoCalObjectFree(BongoCalObject *cal, BOOL freeJson);

BongoCalMethod BongoCalObjectGetMethod(BongoCalObject *cal);
void BongoCalObjectSetMethod(BongoCalObject *cal, BongoCalMethod method);

const char *BongoCalObjectGetCalendarName(BongoCalObject *cal);

const char *BongoCalObjectGetUid(BongoCalObject *cal);

const char *BongoCalObjectGetStamp(BongoCalObject *cal);

BongoCalTime BongoCalObjectGetStart(BongoCalObject *cal);
BongoCalTime BongoCalObjectGetEnd(BongoCalObject *cal);

const char *BongoCalObjectGetSummary(BongoCalObject *cal);
const char *BongoCalObjectGetLocation(BongoCalObject *cal);
const char *BongoCalObjectGetDescription(BongoCalObject *cal);

BongoCalTimezone *BongoCalObjectGetTimezone(BongoCalObject *cal, const char *tzid);
BongoHashtable *BongoCalObjectGetTimezones(BongoCalObject *cal);

void BongoCalObjectResolveSystemTimezones(BongoCalObject *cal);
void BongoCalObjectStripSystemTimezones(BongoCalObject *cal);

BongoArray *BongoCalObjectGetInstances(BongoCalObject *cal);

BOOL BongoCalObjectIsSingle(BongoCalObject *cal);
BongoCalInstance *BongoCalObjectGetSingleInstance(BongoCalObject *cal);

/* Exception/generated instances */

BongoCalInstance *BongoCalObjectGetInstance(BongoCalObject *cal, const char *uid, BongoCalTime recurid);

/* Expand recurrences between a start and end time, returns TRUE if
 * any instances cross the requested times */
BOOL BongoCalObjectCollect(BongoCalObject *cal,
                          BongoCalTime start,
                          BongoCalTime end,
                          BongoCalTimezone *tz,
                          BOOL generate,
                          BongoArray *instances);

/* Return the primary occurrence of a cal object */
BongoCalOccurrence BongoCalObjectPrimaryOccurrence(BongoCalObject *cal,
                                                 BongoCalTimezone *defaultTz);

BongoJsonObject *BongoCalObjectGetJson(BongoCalObject *cal);
BOOL BongoCalObjectAddInstance(BongoCalObject *cal,
                              BongoCalInstance *inst);


/*** BongoCalInstance ***/

BongoCalInstance *BongoCalInstanceNew(BongoCalObject *obj, BongoJsonObject *json);
void BongoCalInstanceInit(BongoCalInstance *instance, BongoCalObject *obj, BongoJsonObject *json);

const char *BongoCalInstanceGetUid(BongoCalInstance *inst);
void BongoCalInstanceSetUid(BongoCalInstance *inst, const char *uid);

BongoCalTime BongoCalInstanceGetRecurId(BongoCalInstance *inst);
void BongoCalInstanceSetRecurId(BongoCalInstance *inst, BongoCalTime recurid);

void BongoCalInstanceFree(BongoCalInstance *instance, BOOL freeJson);
void BongoCalInstanceClear(BongoCalInstance *instance, BOOL freeJson);

BongoCalType BongoCalInstanceGetType(BongoCalInstance *instance);

BOOL BongoCalInstanceIsRecurrence(BongoCalInstance *instance);
BOOL BongoCalInstanceIsGenerated(BongoCalInstance *instance);

BOOL BongoCalInstanceHasStart(BongoCalInstance *instance);
BongoCalTime BongoCalInstanceGetStart(BongoCalInstance *instance);
void BongoCalInstanceSetStart(BongoCalInstance *instance, BongoCalTime t);

BOOL BongoCalInstanceHasEnd(BongoCalInstance *instance);
BongoCalTime BongoCalInstanceGetEnd(BongoCalInstance *instance);
BongoCalTime BongoCalInstanceGetLastEnd(BongoCalInstance *instance);
void BongoCalInstanceSetEnd(BongoCalInstance *instance, BongoCalTime t);

BOOL BongoCalInstanceHasTzOffsetTo(BongoCalInstance *instance);
int BongoCalInstanceGetTzOffsetTo(BongoCalInstance *instance);

BOOL BongoCalInstanceHasTzOffsetFrom(BongoCalInstance *instance);
int BongoCalInstanceGetTzOffsetFrom(BongoCalInstance *instance);

const char *BongoCalInstanceGetSummary(BongoCalInstance *inst);
void BongoCalInstanceSetSummary(BongoCalInstance *inst, const char *summary);

const char *BongoCalInstanceGetStamp(BongoCalInstance *inst);
void BongoCalInstanceSetStamp(BongoCalInstance *inst, const char *stamp);

const char *BongoCalInstanceGetLocation(BongoCalInstance *inst);

const char *BongoCalInstanceGetDescription(BongoCalInstance *inst);

BongoJsonObject *BongoCalInstanceGetJson(BongoCalInstance *cal);

/* Recurrences */ 

BOOL BongoCalInstanceHasRecurrences(BongoCalInstance *inst);

BOOL BongoCalInstanceHasRRules(BongoCalInstance *inst);
BOOL BongoCalInstanceGetRRules(BongoCalInstance *inst, BongoSList **list);

BOOL BongoCalInstanceHasRDates(BongoCalInstance *inst);
BOOL BongoCalInstanceGetRDates(BongoCalInstance *inst, BongoSList **list);

/* Recurrence exceptions */

BOOL BongoCalInstanceHasExceptions(BongoCalInstance *inst);

BOOL BongoCalInstanceHasExRules(BongoCalInstance *inst);
BOOL BongoCalInstanceGetExRules(BongoCalInstance *inst, BongoSList **list);

BOOL BongoCalInstanceHasExDates(BongoCalInstance *inst);
BOOL BongoCalInstanceGetExDates(BongoCalInstance *inst, BongoSList **list);

/* Recurrence expansion */

/* Expand recurrences between a start and end time, returns TRUE if
 * any instances cross the requested times */

BongoCalOccurrence BongoCalInstanceGetOccurrence(BongoCalInstance *instance);

BOOL BongoCalInstanceCrosses(BongoCalInstance *instance,
                            BongoCalTime start,
                            BongoCalTime end);

BongoJsonObject *BongoCalOccurrenceToJson(BongoCalOccurrence occ, BongoCalTimezone *tz);
BongoArray *BongoCalOccurrencesToJson(BongoArray *occs, BongoCalTimezone *tz);
void BongoCalOccurrencesSort(BongoArray *occs);

/*** BongoCalTimezone ***/

BongoCalTimezone *BongoCalTimezoneNewJson(BongoCalObject *obj, 
                                        BongoJsonObject *json,
                                        const char *tzid);
BongoCalTimezone *BongoCalTimezoneNewJsonForSystem(BongoCalObject *cal, 
                                                 const char *tzid);
BongoCalTimezone *BongoCalTimezoneGetUtc(void);
BongoCalTimezone *BongoCalTimezoneGetSystem(const char *tzid);
void BongoCalTimezoneFree(BongoCalTimezone *tz, 
                         BOOL freeJson);

BongoCalTime BongoCalTimezoneConvertTime(BongoCalTime time,
                                       BongoCalTimezone *to);

int BongoCalTimezoneGetUtcOffset(BongoCalTimezone *tz,
                                BongoCalTime t,
                                BOOL timeAsUtc, 
                                BOOL *isDaylight);

const char *BongoCalTimezoneGetTzid(BongoCalTimezone *tz);

BOOL BongoCalIsLeapYear(int year);

void BongoCalTimezoneExpand(BongoCalTimezone *tz, 
                           BongoCalTime start,
                           BongoCalTime end);

BOOL BongoCalTimezonePack(BongoCalTimezone *tz, FILE *to);
BongoCalTimezone *BongoCalTimezoneUnpack(FILE *from);

/*** BongoCalTime ***/

BongoCalTime BongoCalTimeEmpty(void);
BongoCalTime BongoCalTimeNewDate(int day, int month, int year);
BongoCalTime BongoCalTimeNewFromUint64(uint64_t src, BOOL isDate, BongoCalTimezone *tz);
BongoCalTime BongoCalTimeNow(BongoCalTimezone *tz);

BongoCalTime BongoCalTimeSetTimezone(BongoCalTime t, BongoCalTimezone *tz);
BongoCalTime BongoCalTimeSetTzid(BongoCalTime t, const char *tzid);

BOOL BongoCalTimeIsEmpty(BongoCalTime time);
uint64_t BongoCalTimeUtcAsUint64(BongoCalTime time);
uint64_t BongoCalTimeAsUint64(BongoCalTime time);

/* The combined "fix up this time and convert it to uint64" semantics
 * of mktime, for porting ease */
uint64_t BongoCalMkTime(BongoCalTime *t);

BongoCalTime BongoCalTimeParseIcal(const char *str);
void BongoCalTimeToIcal(BongoCalTime t, char *buffer, int buflen);
int BongoCalTimeToStringConcise(BongoCalTime t, char *buffer, int buflen);

BongoCalTime BongoCalTimeAdjust(BongoCalTime t, int days, int hours, int minutes, int seconds);
BongoCalTime BongoCalTimeAdjustDuration(BongoCalTime t, BongoCalDuration d);

int BongoCalTimeCompareDateOnly(BongoCalTime a, BongoCalTime b);
int BongoCalTimeCompare(BongoCalTime a, BongoCalTime b);

int BongoCalTimeGetJulianDay(BongoCalTime t);
int BongoCalTimeGetWeekday(BongoCalTime t);

int BongoCalDaysInMonth(int month, int year);
int BongoCalDaysInMonthNoLeap(int month);
int BongoCalTimeGetDayOfYear(BongoCalTime t);

/*** BongoCalDuration ***/

BongoCalDuration BongoCalDurationEmpty(void);
BOOL BongoCalDurationIsEmpty(BongoCalDuration d);
BongoCalDuration BongoCalDurationParseIcal(const char *str);

/*** Raw json/ical Functions ***/

BongoJsonObject *BongoCalValueString(const char *value);
BongoJsonObject *BongoCalValueTime(BongoCalTime time);

BongoCalType BongoCalTypeFromJson(BongoJsonObject *object);

BongoJsonObject *BongoIcalPropertyToJson(icalproperty *prop);
BongoJsonObject *BongoIcalPropertyToJson(icalproperty *prop);

BongoJsonObject *BongoIcalComponentToJson(icalcomponent *comp, BOOL recurse);
icalcomponent *BongoJsonComponentToIcal(BongoJsonObject *object, icalcomponent *parent, BOOL recurse);

icalcomponent *BongoCalJsonToIcal(BongoJsonObject *obj);
BongoJsonObject *BongoCalIcalToJson(icalcomponent *comp);

/* Split a json calendar object into a json array of calendar objects
 * with one uid per object.  Destroys the original object */
BongoArray *BongoCalSplit(BongoJsonObject *obj);

/* Merge a json calendar object into another one.  Destroys src */
BongoJsonResult BongoCalMerge(BongoJsonObject *dest, BongoJsonObject *src);

XPL_END_C_LINKAGE

#endif
