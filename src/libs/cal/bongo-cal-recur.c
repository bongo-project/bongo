/*
 * Bongo calendar recurrence rule functions.
 * 
 * Based heavily on code from Evolution. 
 *
 * Copyright (C) 2000 Ximian, Inc.
 * Copyright (C) 2006 Novell, Inc.
 *
 * Author: Damon Chaplin <damon@ximian.com>
 *
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2 of the GNU Lesser General Public
 * License as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Lesser General Public License for more details.
 *
 * You should have received a copy of the GNU Lesser General Public License
 * along with this program; if not, write to the Free Software
 * Foundation, Inc., 59 Temple Place, Suite 330, Boston, MA 02111-1307, USA.
 */

#ifdef HAVE_CONFIG_H
#include <config.h>
#endif

#define DEBUG 1
#define DEBUG_RECUR 0

#include <xpl.h>

#include <stdlib.h>
#include <string.h>
#include "bongo-cal-private.h"

#if DEBUG_RECUR
#define DPRINT(...) { printf("%s(%d): ", __FILE__, __LINE__); printf(__VA_ARGS__); }
#else
#define DPRINT(...)
#endif

/*
 * Introduction to The Recurrence Generation Functions:
 *
 * Note: This is pretty complicated. See the iCalendar spec (RFC 2445) for
 *       the specification of the recurrence rules and lots of examples
 *       (sections 4.3.10 & 4.8.5). We also want to support the older
 *       vCalendar spec, though this should be easy since it is basically a
 *       subset of iCalendar.
 *
 * o An iCalendar event can have any number of recurrence rules specifying
 *   occurrences of the event, as well as dates & times of specific
 *   occurrences. It can also have any number of recurrence rules and
 *   specific dates & times specifying exceptions to the occurrences.
 *   So we first merge all the occurrences generated, eliminating any
 *   duplicates, then we generate all the exceptions and remove these to
 *   form the final set of occurrences.
 *
 * o There are 7 frequencies of occurrences: YEARLY, MONTHLY, WEEKLY, DAILY,
 *   HOURLY, MINUTELY & SECONDLY. The 'interval' property specifies the
 *   multiples of the frequency which we step by. We generate a 'set' of
 *   occurrences for each period defined by the frequency & interval.
 *   So for a YEARLY frequency with an interval of 3, we generate a set of
 *   occurrences for every 3rd year. We use complete years here -  any
 *   generated occurrences that occur before the event's start (or after its
 *   end) are just discarded.
 *
 * o There are 8 frequency modifiers: BYMONTH, BYWEEKNO, BYYEARDAY, BYMONTHDAY,
 *   BYDAY, BYHOUR, BYMINUTE & BYSECOND. These can either add extra occurrences
 *   or filter out occurrences. For example 'FREQ=YEARLY;BYMONTH=1,2' produces
 *   2 occurrences for each year rather than the default 1. And
 *   'FREQ=DAILY;BYMONTH=1' filters out all occurrences except those in Jan.
 *   If the modifier works on periods which are less than the recurrence
 *   frequency, then extra occurrences are added, otherwise occurrences are
 *   filtered. So we have 2 functions for each modifier - one to expand events
 *   and the other to filter. We use a table of functions for each frequency
 *   which points to the appropriate function to use for each modifier.
 *
 * o Any number of frequency modifiers can be used in a recurrence rule.
 *   (Though the iCalendar spec says that BYWEEKNO can only be used in a YEARLY
 *   rule, and some modifiers aren't appropriate for some frequencies - e.g.
 *   BYMONTHDAY is not really useful in a WEEKLY frequency, and BYYEARDAY is
 *   not useful in a MONTHLY or WEEKLY frequency).
 *   The frequency modifiers are applied in the order given above. The first 5
 *   modifier rules (BYMONTH, BYWEEKNO, BYYEARDAY, BYMONTHDAY & BYDAY) all
 *   produce the days on which the occurrences take place, and so we have to
 *   compute some of these in parallel rather than sequentially, or we may end
 *   up with too many days.
 *
 * o Note that some expansion functions may produce days which are invalid,
 *   e.g. 31st September, 30th Feb. These invalid days are removed before the
 *   BYHOUR, BYMINUTE & BYSECOND modifier functions are applied.
 *
 * o After the set of occurrences for the frequency interval are generated,
 *   the BYSETPOS property is used to select which of the occurrences are
 *   finally output. If BYSETPOS is not specified then all the occurrences are
 *   output.
 *
 *
 * FIXME: I think there are a few errors in this code:
 *
 *  1) I'm not sure it should be generating events in parallel like it says
 *     above. That needs to be checked.
 *
 *  2) I didn't think about timezone changes when implementing this. I just
 *     assumed all the occurrences of the event would be in local time.
 *     But when clocks go back or forwards due to daylight-saving time, some
 *     special handling may be needed, especially for the shorter frequencies.
 *     e.g. for a MINUTELY frequency it should probably iterate over all the
 *     minutes before and after clocks go back (i.e. some may be the same local
 *     time but have different UTC offsets). For longer frequencies, if an
 *     occurrence lands on the overlapping or non-existant time when clocks
 *     go back/forward, then it may need to choose which of the times to use
 *     or move the time forward or something. I'm not sure this is clear in the
 *     spec.
 */

/* Define this for some debugging output. */
#if 0
#define CAL_OBJ_DEBUG   1
#endif

/* This is what we use to pass to all the filter functions. */
typedef struct _RecurData RecurData;
struct _RecurData {
    BongoCalRule *recur;

    /* This is used for the WEEKLY frequency. It is the offset from the
       week_start_day. */
    int weekdayOffset;

    /* This is used for fast lookup in BYMONTH filtering. */
    uint8_t months[12];

    /* This is used for fast lookup in BYYEARDAY filtering. */
    uint8_t yeardays[367], negYeardays[367]; /* Days are 1 - 366. */

    /* This is used for fast lookup in BYMONTHDAY filtering. */
    uint8_t monthdays[32], negMonthdays[32]; /* Days are 1 to 31. */

    /* This is used for fast lookup in BYDAY filtering. */
    uint8_t weekdays[7];

    /* This is used for fast lookup in BYHOUR filtering. */
    uint8_t hours[24];

    /* This is used for fast lookup in BYMINUTE filtering. */
    uint8_t minutes[60];

    /* This is used for fast lookup in BYSECOND filtering. */
    uint8_t seconds[62];
};

/* This is what we use to represent a date & time. */
typedef struct _CalObjTime CalObjTime;
struct _CalObjTime {
    uint16_t year;
    uint8_t month;          /* 0 - 11 */
    uint8_t day;            /* 1 - 31 */
    uint8_t hour;           /* 0 - 23 */
    uint8_t minute;         /* 0 - 59 */
    uint8_t second;         /* 0 - 59 (maybe up to 61 for leap seconds) */
    uint8_t flags;          /* The meaning of this depends on where the
                               CalObjTime is used. In most cases this is
                               set to TRUE to indicate that this is an
                               RDATE with an end or a duration set.
                               In the exceptions code, this is set to TRUE
                               to indicate that this is an EXDATE with a
                               DATE value. */
};

/* This is what we use to represent specific recurrence dates.
   Note that we assume it starts with a CalObjTime when sorting. */
typedef struct _CalObjRecurrenceDate CalObjRecurrenceDate;
struct _CalObjRecurrenceDate {
    CalObjTime start;
    BongoCalPeriod *period;
};

typedef BOOL (*CalObjFindStartFn) (CalObjTime *eventStart,
                                   CalObjTime *eventEnd,
                                   RecurData  *recurData,
                                   CalObjTime *intervalStart,
                                   CalObjTime *intervalEnd,
                                   CalObjTime *cotime);
typedef BOOL (*CalObjFindNextFn)  (CalObjTime *cotime,
                                   CalObjTime *eventEnd,
                                   RecurData  *recurData,
                                   CalObjTime *intervalEnd);
typedef GArray*       (*CalObjFilterFn)    (RecurData  *recurData,
                                               GArray     *occs);

typedef struct _BongoCalRecurVTable BongoCalRecurVTable;
struct _BongoCalRecurVTable {
    CalObjFindStartFn find_start_position;
    CalObjFindNextFn find_next_position;

    CalObjFilterFn bymonth_filter;
    CalObjFilterFn byweekno_filter;
    CalObjFilterFn byyearday_filter;
    CalObjFilterFn bymonthday_filter;
    CalObjFilterFn byday_filter;
    CalObjFilterFn byhour_filter;
    CalObjFilterFn byminute_filter;
    CalObjFilterFn bysecond_filter;
};


/* This is used to specify which parts of the CalObjTime to compare in
   CalObjTimeCompare(). */
typedef enum {
    CALOBJ_YEAR,
    CALOBJ_MONTH,
    CALOBJ_DAY,
    CALOBJ_HOUR,
    CALOBJ_MINUTE,
    CALOBJ_SECOND
} CalObjTimeComparison;

static void BongoCalRecurGenerateInstancesOfRule (BongoCalInstance *inst,
                                                 BongoCalRule *recur,
                                                 uint64_t start,
                                                 uint64_t end,
                                                 BongoCalRecurInstanceFn cb,
                                                 void *cbData,
                                                 BongoCalRecurResolveTimezoneFn tzCb,
                                                 void *tzCbData,
                                                 BongoCalTimezone *defaultTimezone);

static BOOL CalObjectGetRDateEnd        (CalObjTime     *occ,
                                         GArray              *rdate_periods);
static void     CalObjectComputeDuration        (CalObjTime     *start,
                                                 CalObjTime     *end,
                                                 int            *days,
                                                 int            *seconds);

static BOOL
GenerateInstancesForChunk (BongoCalInstance *inst,
                           uint64_t instDtstart,
                           BongoCalTimezone *zone,
                           BongoSList *rrules,
                           BongoSList *rdates,
                           BongoSList *exrules,
                           BongoSList *exdates,
                           BOOL singleRule,
                           CalObjTime *eventStart,
                           uint64_t intervalStart,
                           CalObjTime *chunkStart,
                           CalObjTime *chunkEnd,
                           int durationDays,
                           int durationSeconds,
                           BOOL convertEndDate,
                           BongoCalRecurInstanceFn cb,
                           void *cbData);

static GArray* CalObjExpandRecurrence        (CalObjTime       *eventStart,
                                                 BongoCalTimezone     *zone,
                                                 BongoCalRule        *recur,
                                                 CalObjTime       *intervalStart,
                                                 CalObjTime       *intervalEnd,
                                                 BOOL     *finished);

static GArray*       CalObjGenerateSetYearly (RecurData      *recurData,
                                                 BongoCalRecurVTable *vtable,
                                                 CalObjTime     *occ);
static GArray*       CalObjGenerateSetMonthly (RecurData      *recurData,
                                                 BongoCalRecurVTable *vtable,
                                                 CalObjTime     *occ);
static GArray*       CalObjGenerateSetDefault        (RecurData      *recurData,
                                                         BongoCalRecurVTable *vtable,
                                                         CalObjTime     *occ);


static BongoCalRecurVTable* CalObjGetVtable      (BongoCalRecurFrequency recurType);
static void     CalObjInitializeRecurData       (RecurData      *recurData,
                                                 BongoCalRule      *recur,
                                                 CalObjTime     *eventStart);
static void     CalObjSortOccurrences   (GArray              *occs);
static int      CalObjCompareFunc       (const void     *arg1,
                                         const void     *arg2);
static void     CalObjRemoveDuplicatesAndInvalidDates (GArray        *occs);
static void     CalObjRemoveExceptions  (GArray              *occs,
                                         GArray              *exOccs);
static GArray*       CalObjBySetPosFilter            (BongoCalRule      *recur,
                                                         GArray              *occs);


static BOOL CalObjYearlyFindStartPosition (CalObjTime *eventStart,
                                           CalObjTime *eventEnd,
                                           RecurData  *recurData,
                                           CalObjTime *intervalStart,
                                           CalObjTime *intervalEnd,
                                           CalObjTime *cotime);
static BOOL CalObjYEarlyFindNextPosition  (CalObjTime *cotime,
                                           CalObjTime *eventEnd,
                                           RecurData  *recurData,
                                           CalObjTime *intervalEnd);

static BOOL CalObjMonthlyFindStartPosition (CalObjTime *eventStart,
                                            CalObjTime *eventEnd,
                                            RecurData  *recurData,
                                            CalObjTime *intervalStart,
                                            CalObjTime *intervalEnd,
                                            CalObjTime *cotime);
static BOOL CalObjMonthlyFindNextPosition  (CalObjTime *cotime,
                                            CalObjTime *eventEnd,
                                            RecurData  *recurData,
                                            CalObjTime *intervalEnd);

static BOOL CalObjWeeklyFindStartPosition (CalObjTime *eventStart,
                                           CalObjTime *eventEnd,
                                           RecurData  *recurData,
                                           CalObjTime *intervalStart,
                                           CalObjTime *intervalEnd,
                                           CalObjTime *cotime);
static BOOL CalObjWeeklyFindNextPosition  (CalObjTime *cotime,
                                           CalObjTime *eventEnd,
                                           RecurData  *recurData,
                                           CalObjTime *intervalEnd);

static BOOL CalObjDailyFindStartPosition  (CalObjTime *eventStart,
                                           CalObjTime *eventEnd,
                                           RecurData  *recurData,
                                           CalObjTime *intervalStart,
                                           CalObjTime *intervalEnd,
                                           CalObjTime *cotime);
static BOOL CalObjDailyFindNextPosition   (CalObjTime *cotime,
                                           CalObjTime *eventEnd,
                                           RecurData  *recurData,
                                           CalObjTime *intervalEnd);

static BOOL CalObjHourlyFindStartPosition (CalObjTime *eventStart,
                                           CalObjTime *eventEnd,
                                           RecurData  *recurData,
                                           CalObjTime *intervalStart,
                                           CalObjTime *intervalEnd,
                                           CalObjTime *cotime);
static BOOL CalObjHourlyFindNextPosition  (CalObjTime *cotime,
                                           CalObjTime *eventEnd,
                                           RecurData  *recurData,
                                           CalObjTime *intervalEnd);

static BOOL CalObjMinutelyFindStartPosition (CalObjTime *eventStart,
                                             CalObjTime *eventEnd,
                                             RecurData  *recurData,
                                             CalObjTime *intervalStart,
                                             CalObjTime *intervalEnd,
                                             CalObjTime *cotime);
static BOOL CalObjMinutelyFindNextPosition  (CalObjTime *cotime,
                                             CalObjTime *eventEnd,
                                             RecurData  *recurData,
                                             CalObjTime *intervalEnd);

static BOOL CalObjSecondlyFindStartPosition (CalObjTime *eventStart,
                                             CalObjTime *eventEnd,
                                             RecurData  *recurData,
                                             CalObjTime *intervalStart,
                                             CalObjTime *intervalEnd,
                                             CalObjTime *cotime);
static BOOL CalObjSecondlyFindNextPosition  (CalObjTime *cotime,
                                             CalObjTime *eventEnd,
                                             RecurData  *recurData,
                                             CalObjTime *intervalEnd);
static GArray* CalObjByMonthExpand           (RecurData  *recurData,
                                                 GArray     *occs);
static GArray* CalObjByMonthFilter           (RecurData  *recurData,
                                                 GArray     *occs);
static GArray* CalObjByWeeknoExpand          (RecurData  *recurData,
                                                 GArray     *occs);
#if 0
/* This isn't used at present. */
static GArray* CalObjByWeeknoFilter          (RecurData  *recurData,
                                                 GArray     *occs);
#endif
static GArray* CalObjByYeardayExpand         (RecurData  *recurData,
                                                 GArray     *occs);
static GArray* CalObjByYeardayFilter         (RecurData  *recurData,
                                                 GArray     *occs);
static GArray* CalObjBymonthdayExpand        (RecurData  *recurData,
                                                 GArray     *occs);
static GArray* CalObjBymonthdayFilter        (RecurData  *recurData,
                                                 GArray     *occs);
static GArray* CalObjByDayExpandYearly       (RecurData  *recurData,
                                                 GArray     *occs);
static GArray* CalObjByDayExpandMonthly      (RecurData  *recurData,
                                                 GArray     *occs);
static GArray* CalObjByDayExpandWeekly       (RecurData  *recurData,
                                                 GArray     *occs);
static GArray* CalObjBydayFilter             (RecurData  *recurData,
                                                 GArray     *occs);
static GArray* CalObjByhourExpand            (RecurData  *recurData,
                                                 GArray     *occs);
static GArray* CalObjByhourFilter            (RecurData  *recurData,
                                                 GArray     *occs);
static GArray* CalObjByminuteExpand          (RecurData  *recurData,
                                                 GArray     *occs);
static GArray* CalObjByminuteFilter          (RecurData  *recurData,
                                                 GArray     *occs);
static GArray* CalObjBysecondExpand          (RecurData  *recurData,
                                                 GArray     *occs);
static GArray* CalObjBysecondFilter          (RecurData  *recurData,
                                                 GArray     *occs);

static void CalObjTimeAddMonths         (CalObjTime *cotime,
                                         int         months);
static void CalObjTimeAddDays           (CalObjTime *cotime,
                                         int         days);
static void CalObjTimeAddHours          (CalObjTime *cotime,
                                         int         hours);
static void CalObjTimeAddMinutes                (CalObjTime *cotime,
                                                 int         minutes);
static void CalObjTimeAddSeconds                (CalObjTime *cotime,
                                                 int         seconds);
static int CalObjTimeCompare            (CalObjTime *cotime1,
                                         CalObjTime *cotime2,
                                         CalObjTimeComparison type);
static int CalObjTimeWeekday            (CalObjTime *cotime);
static int CalObjTimeWeekdayOffset             (CalObjTime *cotime,
                                                 BongoCalRule *recur);
static int CalObjDayOfYear              (CalObjTime *cotime);
static void CalObjFindFirstWeek (CalObjTime *cotime,
                                 RecurData  *recurData);
static void CalObjTimeFromTime          (CalObjTime *cotime,
                                         uint64_t t,
                                         BongoCalTimezone *zone);
static int CalObjDateOnlyCompareFunc    (const void *arg1,
                                         const void *arg2);



static BOOL BongoCalRecurEnsureRuleEndDate       (BongoCalInstance       *inst,
                                                 BongoCalRule   *recur,
                                                 BOOL    exception,
                                                 BOOL    refresh,
                                                 BongoCalRecurResolveTimezoneFn tzCb,
                                                 void *  tzCbData);
static BOOL BongoCalRecurEnsureRuleEndDateCb     (BongoCalInstance       *inst,
                                                 uint64_t          instanceStart,
                                                 uint64_t          instanceEnd,
                                                 void *  data);
static void BongoCalRecurSetRuleEndDate          (BongoCalRule *recur,
                                                 uint64_t endDate);


#if DEBUG_RECUR
static char* CalObjTimeToString         (CalObjTime     *cotime);
static char* BongoCalTimeToString        (BongoCalTime time);
#endif


BongoCalRecurVTable cal_obj_yearly_vtable = {
    CalObjYearlyFindStartPosition,
    CalObjYEarlyFindNextPosition,

    CalObjByMonthExpand,
    CalObjByWeeknoExpand,
    CalObjByYeardayExpand,
    CalObjBymonthdayExpand,
    CalObjByDayExpandYearly,
    CalObjByhourExpand,
    CalObjByminuteExpand,
    CalObjBysecondExpand
};

BongoCalRecurVTable cal_obj_monthly_vtable = {
    CalObjMonthlyFindStartPosition,
    CalObjMonthlyFindNextPosition,

    CalObjByMonthFilter,
    NULL, /* BYWEEKNO is only applicable to YEARLY frequency. */
    NULL, /* BYYEARDAY is not useful in a MONTHLY frequency. */
    CalObjBymonthdayExpand,
    CalObjByDayExpandMonthly,
    CalObjByhourExpand,
    CalObjByminuteExpand,
    CalObjBysecondExpand
};

BongoCalRecurVTable cal_obj_weekly_vtable = {
    CalObjWeeklyFindStartPosition,
    CalObjWeeklyFindNextPosition,

    CalObjByMonthFilter,
    NULL, /* BYWEEKNO is only applicable to YEARLY frequency. */
    NULL, /* BYYEARDAY is not useful in a WEEKLY frequency. */
    NULL, /* BYMONTHDAY is not useful in a WEEKLY frequency. */
    CalObjByDayExpandWeekly,
    CalObjByhourExpand,
    CalObjByminuteExpand,
    CalObjBysecondExpand
};

BongoCalRecurVTable cal_obj_daily_vtable = {
    CalObjDailyFindStartPosition,
    CalObjDailyFindNextPosition,

    CalObjByMonthFilter,
    NULL, /* BYWEEKNO is only applicable to YEARLY frequency. */
    CalObjByYeardayFilter,
    CalObjBymonthdayFilter,
    CalObjBydayFilter,
    CalObjByhourExpand,
    CalObjByminuteExpand,
    CalObjBysecondExpand
};

BongoCalRecurVTable cal_obj_hourly_vtable = {
    CalObjHourlyFindStartPosition,
    CalObjHourlyFindNextPosition,

    CalObjByMonthFilter,
    NULL, /* BYWEEKNO is only applicable to YEARLY frequency. */
    CalObjByYeardayFilter,
    CalObjBymonthdayFilter,
    CalObjBydayFilter,
    CalObjByhourFilter,
    CalObjByminuteExpand,
    CalObjBysecondExpand
};

BongoCalRecurVTable cal_obj_minutely_vtable = {
    CalObjMinutelyFindStartPosition,
    CalObjMinutelyFindNextPosition,

    CalObjByMonthFilter,
    NULL, /* BYWEEKNO is only applicable to YEARLY frequency. */
    CalObjByYeardayFilter,
    CalObjBymonthdayFilter,
    CalObjBydayFilter,
    CalObjByhourFilter,
    CalObjByminuteFilter,
    CalObjBysecondExpand
};

BongoCalRecurVTable cal_obj_secondly_vtable = {
    CalObjSecondlyFindStartPosition,
    CalObjSecondlyFindNextPosition,

    CalObjByMonthFilter,
    NULL, /* BYWEEKNO is only applicable to YEARLY frequency. */
    CalObjByYeardayFilter,
    CalObjBymonthdayFilter,
    CalObjBydayFilter,
    CalObjByhourFilter,
    CalObjByminuteFilter,
    CalObjBysecondFilter
};

#if DEBUG_RECUR
static void 
PrintIntList(BongoList *list)
{
    for (; list != NULL; list = list->next) {
        printf("%d,", (int)(list->data));
    }
}

static void
PrintRecurrence(BongoCalRule *r)
{
    printf("freq: %d\n", r->freq);
    printf("interval: %d\n", r->interval);
    printf("count: %d\n", r->count);
    printf("until: %ld\n", r->until);
    printf("weekStartDay: %d\n", r->weekStartDay);
    printf("bymonth: ");
    PrintIntList(r->bymonth);
    printf("\nbyweekno: ");
    PrintIntList(r->byweekno);
    printf("\nbyyearday: ");
    PrintIntList(r->byyearday);
    printf("\nbymonthday: ");
    PrintIntList(r->bymonthday);
    printf("\nbyday: ");
    PrintIntList(r->byday);
    printf("\nbyhour: ");
    PrintIntList(r->byhour);
    printf("\nbyminute: ");
    PrintIntList(r->byminute);
    printf("\nbysecond: ");
    PrintIntList(r->bysecond);
    printf("\nbysetpos: ");
    PrintIntList(r->bysetpos);
    printf("\n");
}
#endif

/**
 * BongoCalRecurGenerateInstances:
 * \param	inst 	A calendar component object.
 * \param	start	Range start time.
 * \param	end		Range end time.
 * \param	cb		Callback function.
 * \param	cbData	Closure data for the callback function.
 * \param	tzCb	Callback for retrieving timezones.
 * \param	tzCbData	Closure data for the timezone callback.
 * \param	defaultTimezone Default timezone to use when a timezone cannot be found.
 *
 * Calls the given callback function for each occurrence of the event that
 * intersects the range between the given start and end times (the end time is
 * not included). Note that the occurrences may start before the given start
 * time.
 *
 * If the callback routine returns FALSE the occurrence generation stops.
 *
 * Both start and end can be -1, in which case we start at the events first
 * instance and continue until it ends, or forever if it has no enddate.
 *
 * The tzCb is used to resolve references to timezones. It is passed a TZID
 * and should return the BongoCalTimezone* corresponding to that TZID. We need to
 * do this as we access timezones in different ways on the client & server.
 *
 * The defaultTimezone argument is used for DTSTART or DTEND properties that
 * are DATE values or do not have a TZID (i.e. floating times).
 */
void
BongoCalRecurGenerateInstances (BongoCalInstance *inst,
                               uint64_t start,
                               uint64_t end,
                               BongoCalRecurInstanceFn cb,
                               void *cbData,
                               BongoCalRecurResolveTimezoneFn tzCb,
                               void *tzCbData,
                               BongoCalTimezone *defaultTimezone)
{
    DPRINT("In BongoCalRecurGenerateInstances inst: %p\n", inst);
    DPRINT("  start: %li - %s", start, ctime (&start));
    DPRINT("  end  : %li - %s", end, ctime (&end));
    BongoCalRecurGenerateInstancesOfRule (inst, NULL, start, end,
                                         cb, cbData, tzCb, tzCbData,
                                         defaultTimezone);
}


/*
 * Calls the given callback function for each occurrence of the given
 * recurrence rule between the given start and end times. If the rule is NULL
 * it uses all the rules from the component.
 *
 * If the callback routine returns FALSE the occurrence generation stops.
 *
 * The use of the specific rule is for determining the end of a rule when
 * COUNT is set. The callback will count instances and store the enddate
 * when COUNT is reached.
 *
 * Both start and end can be -1, in which case we start at the events first
 * instance and continue until it ends, or forever if it has no enddate.
 */
static void
BongoCalRecurGenerateInstancesOfRule (BongoCalInstance *inst,
                                     BongoCalRule *recur,
                                     uint64_t start,
                                     uint64_t end,
                                     BongoCalRecurInstanceFn cb,
                                     void *cbData,
                                     BongoCalRecurResolveTimezoneFn tzCb,
                                     void *tzCbData,
                                     BongoCalTimezone *defaultTimezone)
{
    BongoCalTime dtstart, dtend;
    uint64_t dtstartTime, dtendTime;
    BongoSList *rrules = NULL, *rdates = NULL, elem;
    BongoSList *exrules = NULL, *exdates = NULL;
    CalObjTime intervalStart, intervalEnd, eventStart, eventEnd;
    CalObjTime chunkStart, chunkEnd;
    int days, seconds, year;
    BOOL singleRule, convertEndDate = FALSE;
    BongoCalTimezone *startZone = NULL, *endZone = NULL;

    BongoReturnIfFail (inst != NULL);
    BongoReturnIfFail (cb != NULL);
    BongoReturnIfFail (tzCb != NULL);
    BongoReturnIfFail (start <= end);

    DPRINT("Generating instances between %ld and %ld\n", start, end);

    /* Get dtstart, dtend, recurrences, and exceptions. Note that
       cal_component_get_dtend() will convert a DURATION property to a
       DTEND so we don't need to worry about that. */

    dtstart = BongoCalInstanceGetStart(inst);
    dtend = BongoCalInstanceGetEnd(inst);

    if (BongoCalTimeIsEmpty(dtstart)) {
        DPRINT ("BongoCalRecurGenerateInstancesOfRule(): bogus "
                "component, does not have DTSTART.  Skipping...\n");
        goto out;
    }

    /* For DATE-TIME values with a TZID, we use the supplied callback to
       resolve the TZID. For DATE values and DATE-TIME values without a
       TZID (i.e. floating times) we use the default timezone. */
    if (!dtstart.tz) {
        if (dtstart.tzid && !dtstart.isDate) {
            startZone = (*tzCb) (dtstart.tzid, tzCbData);
            if (!startZone) {
                startZone = defaultTimezone;
            }
            dtstart = BongoCalTimeSetTimezone(dtstart, startZone);
        } else {
            dtstart = BongoCalTimeSetTimezone(dtstart, defaultTimezone);
            /* Flag that we need to convert the saved ENDDATE property
               to the default timezone. */
            convertEndDate = TRUE;
        }
    }

    startZone = dtstart.tz;

    dtstartTime = BongoCalTimeAsUint64(dtstart);

    if (start == (uint64_t)-1) {
        start = dtstartTime;
    }

    if (!BongoCalTimeIsEmpty(dtend)) {
        /* If both DTSTART and DTEND are DATE values, and they are the
           same day, we add 1 day to DTEND. This means that most
           events created with the old Evolution behavior will still
           work OK. I'm not sure what Outlook does in this case. */
        if (dtstart.isDate && dtend.isDate) {
            if (BongoCalTimeCompareDateOnly(dtstart, dtend) == 0) {
                BongoCalTimeAdjust (dtend, 1, 0, 0, 0);
            }
        }
    } else {
        /* If there is no DTEND, then if DTSTART is a DATE-TIME value
           we use the same time (so we have a single point in time).
           If DTSTART is a DATE value we add 1 day. */
        dtend = dtstart;

        if (dtstart.isDate) {
            BongoCalTimeAdjust(dtend, 1, 0, 0, 0);
        }
    }

    if (!dtend.tz) {
        if (dtend.tzid && !dtend.isDate) {
            endZone = (*tzCb) (dtend.tzid, tzCbData);
            if (!endZone) {
                endZone = defaultTimezone;
            }
            dtend = BongoCalTimeSetTimezone(dtend, endZone);
        } else {
            dtend = BongoCalTimeSetTimezone(dtend, defaultTimezone);
        }
    }
    endZone = dtend.tz;

    dtendTime = BongoCalTimeAsUint64(dtend);

    DPRINT("dtstartTime: %s\n", BongoCalTimeToString(BongoCalTimeNewFromUint64(dtstartTime, FALSE, NULL)));
    DPRINT("dtendTime: %s\n", BongoCalTimeToString(BongoCalTimeNewFromUint64(dtendTime, FALSE, NULL)));

    /* If there is no recurrence, just call the callback if the event
       intersects the given interval. */
    if (!(BongoCalInstanceHasRecurrences (inst)
          || BongoCalInstanceHasExceptions (inst))) {
        DPRINT("no recurrence rules\n");

        if ((end == (uint64_t)-1 || dtstartTime < end) && dtendTime > start) {
            (* cb) (inst, dtstartTime, dtendTime, cbData);
        }

        goto out;
    }

    /* If a specific recurrence rule is being used, set up a simple list,
       else get the recurrence rules from the component. */
    if (recur) {
        DPRINT("specific rule passed in\n");

        singleRule = TRUE;

        elem.data = recur;
        elem.next = NULL;
        rrules = &elem;
    } else if (BongoCalInstanceIsRecurrence (inst)) {
        DPRINT("instance is a recurrence, ignoring\n");
        singleRule = FALSE;
    } else {
        DPRINT("Reading rules from the instance\n");
        singleRule = FALSE;

        /* Make sure all the enddates for the rules are set. */
        BongoCalRecurEnsureEndDates (inst, FALSE, tzCb, tzCbData);

        BongoCalInstanceGetRRules (inst, &rrules);
        BongoCalInstanceGetRDates (inst, &rdates);
        DPRINT("rdates: %p\n", rdates);
        BongoCalInstanceGetExRules (inst, &exrules);
        BongoCalInstanceGetExDates (inst, &exdates);
        DPRINT("exdates: %p\n", exdates);
    }

    /* Convert the interval start & end to CalObjTime. Note that if end
       is -1 intervalEnd won't be set, so don't use it!
       Also note that we use end - 1 since we want the interval to be
       inclusive as it makes the code simpler. We do all calculation
       in the timezone of the DTSTART. */
    CalObjTimeFromTime (&intervalStart, start, startZone);
    if (end != (uint64_t)-1)
        CalObjTimeFromTime (&intervalEnd, end - 1, startZone);

    CalObjTimeFromTime (&eventStart, dtstartTime, startZone);
    CalObjTimeFromTime (&eventEnd, dtendTime, startZone);

    DPRINT("eventStart: %s\n", CalObjTimeToString(&eventStart));
    DPRINT("eventEnd: %s\n", CalObjTimeToString(&eventEnd));    
        
    /* Calculate the duration of the event, which we use for all
       occurrences. We can't just subtract start from end since that may
       be affected by daylight-saving time. So we want a value of days
       + seconds. */
    CalObjectComputeDuration (&eventStart, &eventEnd,
                              &days, &seconds);

    DPRINT("event lasts %d days, %d seconds\n", days, seconds);

    /* Take off the duration from intervalStart, so we get occurrences
       that start just before the start time but overlap it. But only do
       that if the interval is after the event's start time. */
    if (start > dtstartTime) {
        CalObjTimeAddDays (&intervalStart, -days);
        CalObjTimeAddSeconds (&intervalStart, -seconds);
    }

    DPRINT("interval starts at %s\n", CalObjTimeToString(&intervalStart));

    /* Expand the recurrence for each year between start & end, or until
       the callback returns 0 if end is 0. We do a year at a time to
       give the callback function a chance to break out of the loop, and
       so we don't get into problems with infinite recurrences. Since we
       have to work on complete sets of occurrences, if there is a yearly
       frequency it wouldn't make sense to break it into smaller chunks,
       since we would then be calculating the same sets several times.
       Though this does mean that we sometimes do a lot more work than
       is necessary, e.g. if COUNT is set to something quite low. */

    for (year = intervalStart.year;
         (end == (uint64_t)-1 || year <= intervalEnd.year) && year <= MAX_YEAR;
         year++) {
        chunkStart = intervalStart;
        chunkStart.year = year;
        if (end != (uint64_t)-1)
            chunkEnd = intervalEnd;
        chunkEnd.year = year;

        if (year != intervalStart.year) {
            chunkStart.month  = 0;
            chunkStart.day    = 1;
            chunkStart.hour   = 0;
            chunkStart.minute = 0;
            chunkStart.second = 0;
        }
        if (end == (uint64_t)-1 || year != intervalEnd.year) {
            chunkEnd.month  = 11;
            chunkEnd.day    = 31;
            chunkEnd.hour   = 23;
            chunkEnd.minute = 59;
            chunkEnd.second = 61;
            chunkEnd.flags  = FALSE;
        }

        DPRINT("chunk starts at %s\n", CalObjTimeToString(&chunkStart));
        DPRINT("chunk ends at %s\n", CalObjTimeToString(&chunkEnd));

        if (!GenerateInstancesForChunk (inst, dtstartTime,
                                        startZone,
                                        rrules, rdates,
                                        exrules, exdates,
                                        singleRule,
                                        &eventStart,
                                        start,
                                        &chunkStart, &chunkEnd,
                                        days, seconds,
                                        convertEndDate,
                                        cb, cbData))
            break;
    }
out :
    return;
    
}

/* Generates one year's worth of recurrence instances.  Returns TRUE if all the
 * callback invocations returned TRUE, or FALSE when any one of them returns
 * FALSE, i.e. meaning that the instance generation should be stopped.
 *
 * This should only output instances whose start time is between chunkStart
 * and chunkEnd (inclusive), or we may generate duplicates when we do the next
 * chunk. (This applies mainly to weekly recurrences, since weeks can span 2
 * years.)
 *
 * It should also only output instances that are on or after the event's
 * DTSTART property and that intersect the required interval, between
 * intervalStart and intervalEnd.
 */
static BOOL
GenerateInstancesForChunk (BongoCalInstance *inst,
                           uint64_t instDtstart,
                           BongoCalTimezone *zone,
                           BongoSList *rrules,
                           BongoSList *rdates,
                           BongoSList *exrules,
                           BongoSList *exdates,
                           BOOL singleRule,
                           CalObjTime *eventStart,
                           uint64_t intervalStart,
                           CalObjTime *chunkStart,
                           CalObjTime *chunkEnd,
                           int durationDays,
                           int durationSeconds,
                           BOOL convertEndDate,
                           BongoCalRecurInstanceFn cb,
                           void *cbData)
{
    GArray *occs, *exOccs, *tmpOccs, *rdatePeriods;
    CalObjTime cotime, *occ;
    BongoSList *elem;
    unsigned int i;
    uint64_t startTime, endTime;
    BongoCalTime startt, endt;
    BOOL cbStatus = TRUE, ruleFinished, finished = TRUE;

    UNUSED_PARAMETER(convertEndDate)

    DPRINT ("In GenerateInstancesForChunk rrules: %p %p %llud"
            "  %i/%i/%i %02i:%02i:%02i - %i/%i/%i %02i:%02i:%02i\n",
            rrules,
            rdates,
            instDtstart,
            chunkStart->day, chunkStart->month + 1,
            chunkStart->year, chunkStart->hour,
            chunkStart->minute, chunkStart->second,
            chunkEnd->day, chunkEnd->month + 1,
            chunkEnd->year, chunkEnd->hour,
            chunkEnd->minute, chunkEnd->second);

    occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));
    exOccs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));
    rdatePeriods = g_array_new (FALSE, FALSE, sizeof (CalObjRecurrenceDate));

    /* The original DTSTART property is included in the occurrence set,
       but not if we are just generating occurrences for a single rule. */
    if (!singleRule) {
        /* We add it if it is in this chunk. If it is after this chunk
           we set finished to FALSE, since we know we aren't finished
           yet. */
        if (CalObjCompareFunc (eventStart, chunkEnd) >= 0) {
            finished = FALSE;
        } else if (CalObjCompareFunc (eventStart, chunkStart) >= 0) {
            CalObjTime *new_cot;
            DPRINT("adding original DTSTART to the occurrence set\n");
            new_cot = MemNew(CalObjTime, 1);
            memcpy(new_cot, eventStart, sizeof(CalObjTime));
            g_array_append_vals(occs, new_cot, 1);
        }
    }
        
    /* Expand each of the recurrence rules. */
    for (elem = rrules; elem; elem = elem->next) {
        BongoCalRule *r;

        r = elem->data;
        DPRINT("about to expand recurrences\n");
        tmpOccs = CalObjExpandRecurrence (eventStart, zone, r,
                                           chunkStart,
                                           chunkEnd,
                                           &ruleFinished);
        DPRINT("done expanding recurrences\n");
        /* If any of the rules return FALSE for finished, we know we
           have to carry on so we set finished to FALSE. */
        if (!ruleFinished) {
            finished = FALSE;
        }
        DPRINT("added %d occurrences to %d existing occurrences\n", tmpOccs->len, occs->len);

        g_array_append_vals(occs, tmpOccs->data, tmpOccs->len);
        g_array_free(tmpOccs, TRUE);
    }
    DPRINT("finished %d\n", ruleFinished);

    /* Add on specific RDATE occurrence dates. If they have an end time
       or duration set, flag them as RDATEs, and store a pointer to the
       period in the rdatePeriods array. Otherwise we can just treat them
       as normal occurrences. */
    for (elem = rdates; elem; elem = elem->next) {
        BongoCalRDate *p;
        BongoCalTime *cdt;
        CalObjRecurrenceDate rdate;
        CalObjTime *new_cot;

        p = elem->data;
        if (p->type == BONGO_CAL_RDATE_DATETIME) {
            cdt = &p->value.time;
        } else {
            cdt = &p->value.period.start;
        }

        /* FIXME: We currently assume RDATEs are in the same timezone
           as DTSTART. We should get the RDATE timezone and convert
           to the DTSTART timezone first. */
        cotime.year     = cdt->year;
        cotime.month    = cdt->month;
        cotime.day      = cdt->day;
        cotime.hour     = cdt->hour;
        cotime.minute   = cdt->minute;
        cotime.second   = cdt->second;
        cotime.flags    = FALSE;

        /* If the rdate is after the current chunk we set finished
           to FALSE, and we skip it. */
        if (CalObjCompareFunc (&cotime, chunkEnd) >= 0) {
            finished = FALSE;
            continue;
        }

        /* If the rdate is before the current chunk we set finished
           to FALSE, and we skip it. */
        if (CalObjCompareFunc (&cotime, chunkStart) < 0) {
            continue;
        }

        /* Check if the end date or duration is set. If it is we need
           to store it so we can get it later. (libical seems to set
           second to -1 to denote an unset time. See icalvalue.c)
           FIXME. */
        if (p->type != (BongoCalRDateType)BONGO_CAL_PERIOD_DATETIME) {
            CalObjTime *c = MemNew(CalObjTime, 1);
            cotime.flags = TRUE;

            rdate.start = cotime;
            rdate.period = &p->value.period;
            memcpy(c, &rdate, sizeof(CalObjTime));
            g_array_append_vals(rdatePeriods, c, 1);
        }

        new_cot = MemNew(CalObjTime, 1);
        memcpy(new_cot, &cotime, sizeof(CalObjTime));
        g_array_append_vals(occs, new_cot, 1);
    }

    /* Expand each of the exception rules. */
    for (elem = exrules; elem; elem = elem->next) {
        BongoCalRule *r;

        r = elem->data;

        tmpOccs = CalObjExpandRecurrence (eventStart, zone, r,
                                           chunkStart,
                                           chunkEnd,
                                           &ruleFinished);

        DPRINT("adding exrules\n");
        
        g_array_append_vals(exOccs, tmpOccs->data, tmpOccs->len);
        g_array_free(tmpOccs, TRUE);
    }

    /* Add on specific exception dates. */
    for (elem = exdates; elem; elem = elem->next) {
        BongoCalRDate *exdate;
        BongoCalTime *cdt;

        exdate = elem->data;
        if (exdate->type == BONGO_CAL_RDATE_DATETIME) {
            cdt = &exdate->value.time;
        } else {
            cdt = &exdate->value.period.start;
        }
        
        DPRINT("exdate: %s\n", BongoCalTimeToString(*cdt));

        /* FIXME: We currently assume EXDATEs are in the same timezone
           as DTSTART. We should get the EXDATE timezone and convert
           to the DTSTART timezone first. */
        cotime.year     = cdt->year;
        cotime.month    = cdt->month;
        cotime.day      = cdt->day;

        /* If the EXDATE has a DATE value, set the time to the start
           of the day and set flags to TRUE so we know to skip all
           occurrences on that date. */
        if (cdt->isDate) {
            cotime.hour     = 0;
            cotime.minute   = 0;
            cotime.second   = 0;
            cotime.flags    = TRUE;
        } else {
            cotime.hour     = cdt->hour;
            cotime.minute   = cdt->minute;
            cotime.second   = cdt->second;
            cotime.flags    = FALSE;
        }

        DPRINT("adding exdates\n");

        g_array_append_val(exOccs, cotime);
    }

    /* Sort all the arrays. */
    CalObjSortOccurrences (occs);
    CalObjSortOccurrences (exOccs);

    DPRINT("now have %d occurrences\n", occs->len);
    DPRINT("and %d exception occurrences\n", exOccs->len);

    qsort (rdatePeriods->data, rdatePeriods->len,
           sizeof (CalObjRecurrenceDate), CalObjCompareFunc);

    /* Create the final array, by removing the exceptions from the
       occurrences, and removing any duplicates. */
    CalObjRemoveExceptions (occs, exOccs);

    DPRINT("removed exceptions, now have %d occurrences\n", occs->len);

    /* Call the callback for each occurrence. If it returns 0 we break
       out of the loop. */
    for (i = 0; i < occs->len; i++) {
        /* Convert each CalObjTime into a start & end time, and
           check it is within the bounds of the event & interval. */
        occ = &g_array_index(occs, CalObjTime, i);

        DPRINT ("Checking occurrence: %s\n",
                 CalObjTimeToString (occ));

        startt = BongoCalTimeEmpty();
        startt.year   = occ->year;
        startt.month  = occ->month;
        startt.day    = occ->day;
        startt.hour   = occ->hour;
        startt.minute = occ->minute;
        startt.second = occ->second;
        startt = BongoCalTimeSetTimezone(startt, zone);
        startTime = BongoCalTimeAsUint64(startt);

        if (startTime == (uint64_t)-1) {
            DPRINT("time out of range");
            finished = TRUE;
            break;
        }

        /* Check to ensure that the start time is at or after the
           event's DTSTART time, and that it is inside the chunk that
           we are currently working on. (Note that the chunkEnd time
           is never after the interval end time, so this also tests
           that we don't go past the end of the required interval). */
        if (startTime < instDtstart
            || CalObjCompareFunc (occ, chunkStart) < 0
            || CalObjCompareFunc (occ, chunkEnd) > 0) {
#if DEBUG_RECUR
            char buffer[1024];
            DPRINT ("start time invalid: startTime: %lld instDtstart: %llu\n", startTime, instDtstart);

            BongoCalTimeToIcal(BongoCalTimeNewFromUint64(startTime, FALSE, NULL), buffer, 1024);
            printf("startTime: %s\n", buffer);
            BongoCalTimeToIcal(BongoCalTimeNewFromUint64(instDtstart, FALSE, NULL), buffer, 1024);
            printf("instDtstartTime: %s\n", buffer);
#endif
            continue;
        }

        DPRINT("start time was %s\n", CalObjTimeToString(occ));
        DPRINT("duration is %d days %d seconds\n", durationDays, durationSeconds);

        if (occ->flags) {
            /* If it is an RDATE, we see if the end date or
               duration was set. If not, we use the same duration
               as the original occurrence. */
            if (!CalObjectGetRDateEnd (occ, rdatePeriods)) {
                CalObjTimeAddDays (occ, durationDays);
                CalObjTimeAddSeconds (occ,
                                      durationSeconds);
            }
        } else {
            CalObjTimeAddDays (occ, durationDays);
            CalObjTimeAddSeconds (occ, durationSeconds);
        }

        DPRINT("end time is %s\n", CalObjTimeToString(occ));

        endt = BongoCalTimeEmpty();
        endt.year   = occ->year;
        endt.month  = occ->month;
        endt.day    = occ->day;
        endt.hour   = occ->hour;
        endt.minute = occ->minute;
        endt.second = occ->second;
        endt = BongoCalTimeSetTimezone(endt, zone);
        endTime = BongoCalTimeAsUint64(endt);

        if (endTime == (uint64_t)-1) {
            DPRINT ("time out of range");
            finished = TRUE;
            break;
        }

        /* Check that the end time is after the interval start, so we
           know that it intersects the required interval. */
        if (endTime < intervalStart) {
            DPRINT ("end time invalid: interval start is %d, endTime is %d\n", intervalStart, endTime);
            continue;
        }

        DPRINT("calling callback: %ld %ld!\n", startTime, endTime);
        cbStatus = (*cb) (inst, startTime, endTime, cbData);
        if (!cbStatus)
            break;
    }

    g_array_free(occs, TRUE);
    g_array_free(exOccs, TRUE);
    g_array_free(rdatePeriods, TRUE);

    /* We return TRUE (i.e. carry on) only if the callback has always
       returned TRUE and we know that we have more occurrences to generate
       (i.e. finished is FALSE). */
    return cbStatus && !finished;
}


/* This looks up the occurrence time in the sorted rdatePeriods array, and
   tries to compute the end time of the occurrence. If no end time or duration
   is set it returns FALSE and the default duration will be used. */
static BOOL
CalObjectGetRDateEnd    (CalObjTime     *occ,
                         GArray              *rdatePeriods)
{
    CalObjRecurrenceDate *rdate = NULL;
    BongoCalPeriod *p;
    int lower, upper, middle, cmp = 0;

    lower = 0;
    upper = rdatePeriods->len;

    while (lower < upper) {
        middle = (lower + upper) >> 1;
          
        rdate = &g_array_index(rdatePeriods, CalObjRecurrenceDate,
                                 middle);

        cmp = CalObjCompareFunc (occ, &rdate->start);
          
        if (cmp == 0)
            break;
        else if (cmp < 0)
            upper = middle;
        else
            lower = middle + 1;
    }

    /* This should never happen. */
    if (cmp == 0) {
#if BONGO_COMPLETE
        g_warning ("Recurrence date not found");
#endif
        return FALSE;
    }

    p = rdate->period;
    if (p->type == BONGO_CAL_PERIOD_DATETIME) {
        /* FIXME: We currently assume RDATEs are in the same timezone
           as DTSTART. We should get the RDATE timezone and convert
           to the DTSTART timezone first. */
        occ->year     = p->value.end.year;
        occ->month    = p->value.end.month;
        occ->day      = p->value.end.day;
        occ->hour     = p->value.end.hour;
        occ->minute   = p->value.end.minute;
        occ->second   = p->value.end.second;
        occ->flags    = FALSE;
    } else {
        CalObjTimeAddDays (occ, p->value.duration.weeks * 7
                           + p->value.duration.days);
        CalObjTimeAddHours (occ, p->value.duration.hours);
        CalObjTimeAddMinutes (occ, p->value.duration.minutes);
        CalObjTimeAddSeconds (occ, p->value.duration.seconds);
    }

    return TRUE;
}


static void
CalObjectComputeDuration (CalObjTime *start,
                          CalObjTime *end,
                          int   *days,
                          int   *seconds)
{
    BongoCalTime startDate, endDate;
    int startSeconds, endSeconds;

    startDate = BongoCalTimeNewDate(start->day, start->month, start->year);
    endDate = BongoCalTimeNewDate(end->day, end->month, end->year);

    *days = BongoCalTimeGetJulianDay (endDate) - BongoCalTimeGetJulianDay (startDate);
    startSeconds = start->hour * 3600 + start->minute * 60
        + start->second;
    endSeconds = end->hour * 3600 + end->minute * 60 + end->second;

    *seconds = endSeconds - startSeconds;
    if (*seconds < 0) {
        *days = *days - 1;
        *seconds += 24 * 60 * 60;
    }
}


/* Returns an unsorted GArray of CalObjTime's resulting from expanding the
   given recurrence rule within the given interval. Note that it doesn't
   clip the generated occurrences to the interval, i.e. if the interval
   starts part way through the year this function still returns all the
   occurrences for the year. Clipping is done later.
   The finished flag is set to FALSE if there are more occurrences to generate
   after the given interval.*/
static GArray*
CalObjExpandRecurrence          (CalObjTime *eventStart,
                                 BongoCalTimezone *zone,
                                 BongoCalRule *recur,
                                 CalObjTime *intervalStart,
                                 CalObjTime *intervalEnd,
                                 BOOL *finished)
{
    BongoCalRecurVTable *vtable;
    CalObjTime *eventEnd = NULL, eventEndCotime;
    RecurData recurData;
    CalObjTime occ, *cotime;
    GArray *allOccs, *occs;
    int len;

    /* This is the resulting array of CalObjTime elements. */
    allOccs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));

    *finished = TRUE;

    DPRINT("expanding: \n");
#if DEBUG_RECUR
    PrintRecurrence(recur);
#endif

    DPRINT("getting vtable for %d\n", recur->freq);
    vtable = CalObjGetVtable (recur->freq);
    if (!vtable)
        return allOccs;

    /* Calculate some useful data such as some fast lookup tables. */
    CalObjInitializeRecurData (&recurData, recur, eventStart);

    /* Compute the eventEnd, if the recur's enddate is set. */
    DPRINT("recur->until: %d\n", recur->until);
    if (recur->until > 0) {
        DPRINT("computing event end\n");
        CalObjTimeFromTime (&eventEndCotime,
                            recur->until, zone);
        eventEnd = &eventEndCotime;

        /* If the enddate is before the requested interval return. */
        if (CalObjCompareFunc (eventEnd, intervalStart) < 0)
            return allOccs;
    }

    /* Set finished to FALSE if we know there will be more occurrences to
       do after this interval. */
    if (!intervalEnd || !eventEnd
        || CalObjCompareFunc (eventEnd, intervalEnd) > 0)
        *finished = FALSE;

    DPRINT("finding start position\n");
    /* Get the first period based on the frequency and the interval that
       intersects the interval between start and end. */
    if ((*vtable->find_start_position) (eventStart, eventEnd,
                                        &recurData,
                                        intervalStart, intervalEnd,
                                        &occ)) {
        DPRINT("done finding start position\n");

        return allOccs;
    }

    DPRINT("occurrence time is %s\n", CalObjTimeToString(&occ));

    /* Loop until the event ends or we go past the end of the required
       interval. */
    for (;;) {
        /* Generate the set of occurrences for this period. */
        switch (recur->freq) {
        case BONGO_CAL_RECUR_YEARLY:
            occs = CalObjGenerateSetYearly (&recurData,
                                            vtable, &occ);
            break;
        case BONGO_CAL_RECUR_MONTHLY:
            occs = CalObjGenerateSetMonthly (&recurData,
                                            vtable, &occ);
            break;
        default:
            occs = CalObjGenerateSetDefault (&recurData,
                                             vtable, &occ);
            break;
        }

        DPRINT("after generating set: %d items\n", occs->len);

        /* Sort the occurrences and remove duplicates. */
        CalObjSortOccurrences (occs);
        CalObjRemoveDuplicatesAndInvalidDates (occs);

        /* Apply the BYSETPOS property. */
        occs = CalObjBySetPosFilter (recur, occs);

        /* Remove any occs after eventEnd. */
        len = occs->len - 1;
        if (eventEnd) {
            while (len >= 0) {
                cotime = &g_array_index(occs, CalObjTime, len);
                if (CalObjCompareFunc (cotime, eventEnd) <= 0)
                    break;
                len--;
            }
        }

        /* Add the occurrences onto the main array. */
        if (len >= 0)
            g_array_append_vals(allOccs, occs->data, len + 1);

        g_array_free(occs, TRUE);

        DPRINT("finding next position\n");
        /* Skip to the next period, or exit the loop if finished. */
        if ((*vtable->find_next_position) (&occ, eventEnd,
                                           &recurData, intervalEnd)) {
            
            DPRINT("done finding next position\n");
            break;
        }
        DPRINT("next occurrence time is %s\n", CalObjTimeToString(&occ));
    }

    return allOccs;
}


static GArray*
CalObjGenerateSetYearly (RecurData *recurData,
                         BongoCalRecurVTable *vtable,
                         CalObjTime *occ)
{
    BongoCalRule *recur = recurData->recur;
    GArray *occsArrays[4], *occs, *occs2;
    int numOccsArrays = 0, i;

    /* This is a bit complicated, since the iCalendar spec says that
       several BYxxx modifiers can be used simultaneously. So we have to
       be quite careful when determining the days of the occurrences.
       The BYHOUR, BYMINUTE & BYSECOND modifiers are no problem at all.

       The modifiers we have to worry about are: BYMONTH, BYWEEKNO,
       BYYEARDAY, BYMONTHDAY & BYDAY. We can't do these sequentially
       since each filter will mess up the results of the previous one.
       But they aren't all completely independant, e.g. BYMONTHDAY and
       BYDAY are related to BYMONTH, and BYDAY is related to BYWEEKNO.

       BYDAY & BYMONTHDAY can also be applied independently, which makes
       it worse. So we assume that if BYMONTH or BYWEEKNO is used, then
       the BYDAY modifier applies to those, else it is applied
       independantly.

       We expand the occurrences in parallel into the occsArrays[] array,
       and then merge them all into one GArray before expanding BYHOUR,
       BYMINUTE & BYSECOND. */

    if (recur->bymonth) {
        occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));
        g_array_append_vals(occs, occ, 1);

        occs = (*vtable->bymonth_filter) (recurData, occs);

        /* If BYMONTHDAY & BYDAY are both set we need to expand them
           in parallel and add the results. */
        if (recur->bymonthday && recur->byday) {
            /* Copy the occs array. */
            occs2 = g_array_new(FALSE, FALSE, sizeof (CalObjTime));
            g_array_append_vals(occs2, occs->data, occs->len);

            occs = (*vtable->bymonthday_filter) (recurData, occs);
            /* Note that we explicitly call the monthly version
               of the BYDAY expansion filter. */
            occs2 = CalObjByDayExpandMonthly (recurData,
                                              occs2);

            /* Add the 2 resulting arrays together. */
            g_array_append_vals(occs, occs2->data, occs2->len);
            g_array_free(occs2, TRUE);
        } else {
            occs = (*vtable->bymonthday_filter) (recurData, occs);
            /* Note that we explicitly call the monthly version
               of the BYDAY expansion filter. */
            occs = CalObjByDayExpandMonthly (recurData, occs);
        }

        DPRINT("yearly adding array from bymonth: %d\n", occs->len);
        
        occsArrays[numOccsArrays++] = occs;
    }

    if (recur->byweekno) {
        occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));
        g_array_append_vals(occs, occ, 1);

        occs = (*vtable->byweekno_filter) (recurData, occs);
        /* Note that we explicitly call the weekly version of the
           BYDAY expansion filter. */
        DPRINT("yearly got array from byweekno: %d\n", occs->len);
        occs = CalObjByDayExpandWeekly (recurData, occs);
        DPRINT("yearly adding array from byweekno: %d\n", occs->len);

        occsArrays[numOccsArrays++] = occs;
    }

    if (recur->byyearday) {
        occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));
        g_array_append_vals(occs, occ, 1);

        occs = (*vtable->byyearday_filter) (recurData, occs);

        DPRINT("yearly adding array from byyearday\n");
        occsArrays[numOccsArrays++] = occs;
    }

    /* If BYMONTHDAY is set, and BYMONTH is not set, we need to
       expand BYMONTHDAY independantly. */
    if (recur->bymonthday && !recur->bymonth) {
        occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));
        g_array_append_vals(occs, occ, 1);

        occs = (*vtable->bymonthday_filter) (recurData, occs);

        DPRINT("yearly adding array from bymonthday && !bymonth\n");

        occsArrays[numOccsArrays++] = occs;
    }

    /* If BYDAY is set, and BYMONTH and BYWEEKNO are not set, we need to
       expand BYDAY independantly. */
    if (recur->byday && !recur->bymonth && !recur->byweekno) {
        occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));
        g_array_append_vals(occs, occ, 1);

        occs = (*vtable->byday_filter) (recurData, occs);

        DPRINT("yearly adding array from byday && !bymonth && !byweekno\n");
        occsArrays[numOccsArrays++] = occs;
    }

    /* Add all the arrays together. If no filters were used we just
       create an array with one element. */
    if (numOccsArrays > 0) {
        occs = occsArrays[0];
        for (i = 1; i < numOccsArrays; i++) {
            occs2 = occsArrays[i];
            g_array_append_vals(occs, occs2->data, occs2->len);
            g_array_free(occs2, TRUE);
        }
    } else {
        occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));
        g_array_append_vals(occs, occ, 1);
    }

    /* Now expand BYHOUR, BYMINUTE & BYSECOND. */
    occs = (*vtable->byhour_filter) (recurData, occs);
    occs = (*vtable->byminute_filter) (recurData, occs);
    occs = (*vtable->bysecond_filter) (recurData, occs);

    return occs;
}


static GArray*
CalObjGenerateSetMonthly (RecurData *recurData,
                         BongoCalRecurVTable *vtable,
                         CalObjTime *occ)
{
    GArray *occs, *occs2;

    /* We start with just the one time in each set. */
    occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));
    g_array_append_vals(occs, occ, 1);

    occs = (*vtable->bymonth_filter) (recurData, occs);

    /* We need to combine the output of BYMONTHDAY & BYDAY, by doing them
       in parallel rather than sequentially. If we did them sequentially
       then we would lose the occurrences generated by BYMONTHDAY, and
       instead have repetitions of the occurrences from BYDAY. */
    if (recurData->recur->bymonthday && recurData->recur->byday) {
        occs2 = g_array_new(FALSE, FALSE, sizeof(CalObjTime));
        g_array_append_vals(occs2, occs->data, occs->len);

        occs = (*vtable->bymonthday_filter) (recurData, occs);
        occs2 = (*vtable->byday_filter) (recurData, occs2);

        g_array_append_vals(occs, occs2->data, occs2->len);
        g_array_free(occs2, TRUE);
    } else {
        occs = (*vtable->bymonthday_filter) (recurData, occs);
        occs = (*vtable->byday_filter) (recurData, occs);
    }

    occs = (*vtable->byhour_filter) (recurData, occs);
    occs = (*vtable->byminute_filter) (recurData, occs);
    occs = (*vtable->bysecond_filter) (recurData, occs);

    return occs;
}


static GArray*
CalObjGenerateSetDefault        (RecurData *recurData,
                                 BongoCalRecurVTable *vtable,
                                 CalObjTime *occ)
{
    GArray *occs;

    DPRINT ("Generating set for %i/%i/%i %02i:%02i:%02i\n",
            occ->day, occ->month + 1, occ->year, occ->hour, occ->minute,
            occ->second);

    /* We start with just the one time in the set. */
    occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));
    g_array_append_vals(occs, occ, 1);

    occs = (*vtable->bymonth_filter) (recurData, occs);
    if (vtable->byweekno_filter)
        occs = (*vtable->byweekno_filter) (recurData, occs);
    if (vtable->byyearday_filter)
        occs = (*vtable->byyearday_filter) (recurData, occs);
    if (vtable->bymonthday_filter)
        occs = (*vtable->bymonthday_filter) (recurData, occs);
    occs = (*vtable->byday_filter) (recurData, occs);

    occs = (*vtable->byhour_filter) (recurData, occs);
    occs = (*vtable->byminute_filter) (recurData, occs);
    occs = (*vtable->bysecond_filter) (recurData, occs);

    return occs;
}



/* Returns the function table corresponding to the recurrence frequency. */
static BongoCalRecurVTable* CalObjGetVtable (BongoCalRecurFrequency recurType)
{
    BongoCalRecurVTable* vtable;

    switch (recurType) {
    case BONGO_CAL_RECUR_YEARLY:
        vtable = &cal_obj_yearly_vtable;
        break;
    case BONGO_CAL_RECUR_MONTHLY:
        vtable = &cal_obj_monthly_vtable;
        break;
    case BONGO_CAL_RECUR_WEEKLY:
        vtable = &cal_obj_weekly_vtable;
        break;
    case BONGO_CAL_RECUR_DAILY:
        vtable = &cal_obj_daily_vtable;
        break;
    case BONGO_CAL_RECUR_HOURLY:
        vtable = &cal_obj_hourly_vtable;
        break;
    case BONGO_CAL_RECUR_MINUTELY:
        vtable = &cal_obj_minutely_vtable;
        break;
    case BONGO_CAL_RECUR_SECONDLY:
        vtable = &cal_obj_secondly_vtable;
        break;
    default:
#if BONGO_COMPLETE
        g_warning ("Unknown recurrence frequency");
#endif
        vtable = NULL;
    }

    return vtable;
}


/* This creates a number of fast lookup tables used when filtering with the
   modifier properties BYMONTH, BYYEARDAY etc. */
static void
CalObjInitializeRecurData (RecurData  *recurData,
                           BongoCalRule *recur,
                           CalObjTime *eventStart)
{
    BongoList *elem;
    int month, yearday, monthday, weekday, week_num, hour, minute, second;

    /* Clear the entire RecurData. */
    memset (recurData, 0, sizeof (RecurData));

    recurData->recur = recur;

    /* Set the weekday, used for the WEEKLY frequency and the BYWEEKNO
       modifier. */
    recurData->weekdayOffset = CalObjTimeWeekdayOffset (eventStart,
                                                           recur);

    /* Create an array of months from bymonths for fast lookup. */
    elem = recur->bymonth;
    while (elem) {
        month = BongoListVoidToInt(elem->data);
        recurData->months[month] = 1;
        elem = elem->next;
    }

    /* Create an array of yeardays from byyearday for fast lookup.
       We create a second array to handle the negative values. The first
       element there corresponds to the last day of the year. */
    elem = recur->byyearday;
    while (elem) {
        yearday = BongoListVoidToInt(elem->data);
        if (yearday >= 0)
            recurData->yeardays[yearday] = 1;
        else
            recurData->negYeardays[-yearday] = 1;
        elem = elem->next;
    }

    /* Create an array of monthdays from bymonthday for fast lookup.
       We create a second array to handle the negative values. The first
       element there corresponds to the last day of the month. */
    elem = recur->bymonthday;
    while (elem) {
        monthday = BongoListVoidToInt(elem->data);
        if (monthday >= 0)
            recurData->monthdays[monthday] = 1;
        else
            recurData->negMonthdays[-monthday] = 1;
        elem = elem->next;
    }

    /* Create an array of weekdays from byday for fast lookup. */
    elem = recur->byday;
    while (elem) {
        weekday = BongoListVoidToInt(elem->data);
        elem = elem->next;
        /* The week number is not used when filtering. */
        week_num = BongoListVoidToInt(elem->data);
        elem = elem->next;

        recurData->weekdays[weekday] = 1;
    }

    /* Create an array of hours from byhour for fast lookup. */
    elem = recur->byhour;
    while (elem) {
        hour = BongoListVoidToInt(elem->data);
        recurData->hours[hour] = 1;
        elem = elem->next;
    }

    /* Create an array of minutes from byminutes for fast lookup. */
    elem = recur->byminute;
    while (elem) {
        minute = BongoListVoidToInt(elem->data);
        recurData->minutes[minute] = 1;
        elem = elem->next;
    }

    /* Create an array of seconds from byseconds for fast lookup. */
    elem = recur->bysecond;
    while (elem) {
        second = BongoListVoidToInt(elem->data);
        recurData->seconds[second] = 1;
        elem = elem->next;
    }
}


static void
CalObjSortOccurrences (GArray *occs)
{
    qsort (occs->data, occs->len, sizeof (CalObjTime),
           CalObjCompareFunc);
}


static void
CalObjRemoveDuplicatesAndInvalidDates (GArray *occs)
{
    static const int days_in_month[12] = {
        31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31
    };

    CalObjTime *occ, *prev_occ = NULL;
    int len, i, j = 0, year, month, days;
    BOOL keep_occ;

    len = occs->len;
    for (i = 0; i < len; i++) {
        occ = &g_array_index(occs, CalObjTime, i);
        keep_occ = TRUE;

        if (prev_occ && CalObjCompareFunc (occ,
                                           prev_occ) == 0)
            keep_occ = FALSE;

        year = occ->year;
        month = occ->month;
        days = days_in_month[occ->month];
        /* If it is february and a leap year, add a day. */
        if (month == 1 && (year % 4 == 0
                           && (year % 100 != 0
                               || year % 400 == 0)))
            days++;

        if (occ->day > days) {
            /* move occurrence to the last day of the month */
            occ->day = days;
        }

        if (keep_occ) {
            if (i != j)
                g_array_index(occs, CalObjTime, j)
                    = g_array_index(occs, CalObjTime, i);
            j++;
        }

        prev_occ = occ;
    }

    g_array_set_size(occs, j);
}


/* Removes the exceptions from the exOccs array from the occurrences in the
   occs array, and removes any duplicates. Both arrays are sorted. */
static void
CalObjRemoveExceptions (GArray *occs,
                        GArray *exOccs)
{
    CalObjTime *occ, *prevOcc = NULL, *exOcc = NULL, *lastOccKept;
    int i, j = 0, cmp, exIndex, occsLen, exOccsLen;
    BOOL keepOcc, currentTimeIsException = FALSE;

    if (occs->len == 0)
        return;

    exIndex = 0;
    occsLen = occs->len;
    exOccsLen = exOccs->len;

    if (exOccsLen > 0)
        exOcc = &g_array_index(exOccs, CalObjTime, exIndex);

    for (i = 0; i < occsLen; i++) {
        occ = &g_array_index(occs, CalObjTime, i);
        keepOcc = TRUE;

        /* If the occurrence is a duplicate of the previous one, skip
           it. */
        if (prevOcc
            && CalObjCompareFunc (occ, prevOcc) == 0) {
            DPRINT("removing duplicate occurrence\n");
            keepOcc = FALSE;

            /* If this occurrence is an RDATE with an end or
               duration set, and the previous occurrence in the
               array was kept, set the RDATE flag of the last one,
               so we still use the end date or duration. */
            if (occ->flags && !currentTimeIsException) {
                lastOccKept = &g_array_index(occs,
                                                 CalObjTime,
                                                 j - 1);
                lastOccKept->flags = TRUE;
            }
        } else {
            /* We've found a new occurrence time. Reset the flag
               to indicate that it hasn't been found in the
               exceptions array (yet). */
            currentTimeIsException = FALSE;

            if (exOcc) {
                /* Step through the exceptions until we come
                   to one that matches or follows this
                   occurrence. */
                while (exOcc) {
                    /* If the exception is an EXDATE with
                       a DATE value, we only have to
                       compare the date. */
                    if (exOcc->flags) {
                        DPRINT("date only\n");
                        cmp = CalObjDateOnlyCompareFunc (exOcc, occ);
                    } else {
                        DPRINT("date and time: %04i vs. %d\n", exOcc->year, occ->year);
                        cmp = CalObjCompareFunc (exOcc, occ);
                    }
                    DPRINT("checking occurrence %s against exception %s, got %d\n", strdup(CalObjTimeToString(occ)), strdup(CalObjTimeToString(exOcc)), cmp);

                    if (cmp > 0)
                        break;

                    /* Move to the next exception, or set
                       exOcc to NULL when we reach the
                       end of array. */
                    exIndex++;
                    if (exIndex < exOccsLen)
                        exOcc = &g_array_index(exOccs, CalObjTime, exIndex);
                    else
                        exOcc = NULL;

                    /* If the exception did match this
                       occurrence we remove it, and set the
                       flag to indicate that the current
                       time is an exception. */
                    if (cmp == 0) {
                        DPRINT("was an exception!\n");
                        currentTimeIsException = TRUE;
                        keepOcc = FALSE;
                        break;
                    }
                }
            }
        }

        if (keepOcc) {
            /* We are keeping this occurrence, so we move it to
               the next free space, unless its position hasn't
               changed (i.e. all previous occurrences were also
               kept). */
            if (i != j)
                g_array_index(occs, CalObjTime, j)
                    = g_array_index(occs, CalObjTime, i);
            j++;
        }

        prevOcc = occ;
    }

    g_array_set_size(occs, j);
}



static GArray*
CalObjBySetPosFilter (BongoCalRule *recur,
                      GArray     *occs)
{
    GArray *new_occs;
    CalObjTime *occ;
    BongoList *elem;
    int len, pos;

    /* If BYSETPOS has not been specified, or the array is empty, just
       return the array. */
    elem = recur->bysetpos;
    if (!elem || occs->len == 0)
        return occs;

    new_occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));

    /* Iterate over the indices given in bysetpos, adding the corresponding
       element from occs to new_occs. */
    len = occs->len;
    while (elem) {
        pos = BongoListVoidToInt(elem->data);

        /* Negative values count back from the end of the array. */
        if (pos < 0)
            pos += len;
        /* Positive values need to be decremented since the array is
           0-based. */
        else
            pos--;

        if (pos >= 0 && pos < len) {
            occ = &g_array_index(occs, CalObjTime, pos);
            g_array_append_vals(new_occs, occ, 1);
        }
        elem = elem->next;
    }

    g_array_free(occs, TRUE);

    return new_occs;
}




/* Finds the first year from the eventStart, counting in multiples of the
   recurrence interval, that intersects the given interval. It returns TRUE
   if there is no intersection. */
static BOOL
CalObjYearlyFindStartPosition (CalObjTime *eventStart,
                               CalObjTime *eventEnd,
                               RecurData  *recurData,
                               CalObjTime *intervalStart,
                               CalObjTime *intervalEnd,
                               CalObjTime *cotime)
{
    *cotime = *eventStart;

    /* Move on to the next interval, if the event starts before the
       given interval. */
    if (cotime->year < intervalStart->year) {
        int years = intervalStart->year - cotime->year
            + recurData->recur->interval - 1;
        years -= years % recurData->recur->interval;
        /* NOTE: The day may now be invalid, e.g. 29th Feb. */
        cotime->year += years;
    }

    if ((eventEnd && cotime->year > eventEnd->year)
        || (intervalEnd && cotime->year > intervalEnd->year))
        return TRUE;

    return FALSE;
}


static BOOL
CalObjYEarlyFindNextPosition (CalObjTime *cotime,
                              CalObjTime *eventEnd,
                              RecurData  *recurData,
                              CalObjTime *intervalEnd)
{
    /* NOTE: The day may now be invalid, e.g. 29th Feb. */
    cotime->year += recurData->recur->interval;

    if ((eventEnd && cotime->year > eventEnd->year)
        || (intervalEnd && cotime->year > intervalEnd->year))
        return TRUE;

    return FALSE;
}



static BOOL
CalObjMonthlyFindStartPosition (CalObjTime *eventStart,
                                CalObjTime *eventEnd,
                                RecurData  *recurData,
                                CalObjTime *intervalStart,
                                CalObjTime *intervalEnd,
                                CalObjTime *cotime)
{
    *cotime = *eventStart;

    /* Move on to the next interval, if the event starts before the
       given interval. */
    if (CalObjTimeCompare (cotime, intervalStart, CALOBJ_MONTH) < 0) {
        int months = (intervalStart->year - cotime->year) * 12
            + intervalStart->month - cotime->month
            + recurData->recur->interval - 1;
        months -= months % recurData->recur->interval;
        /* NOTE: The day may now be invalid, e.g. 31st Sep. */
        CalObjTimeAddMonths (cotime, months);
    }

    if (eventEnd && CalObjTimeCompare (cotime, eventEnd,
                                        CALOBJ_MONTH) > 0)
        return TRUE;
    if (intervalEnd && CalObjTimeCompare (cotime, intervalEnd,
                                           CALOBJ_MONTH) > 0)
        return TRUE;

    return FALSE;
}


static BOOL
CalObjMonthlyFindNextPosition (CalObjTime *cotime,
                               CalObjTime *eventEnd,
                               RecurData  *recurData,
                               CalObjTime *intervalEnd)
{
    /* NOTE: The day may now be invalid, e.g. 31st Sep. */
    CalObjTimeAddMonths (cotime, recurData->recur->interval);

    if (eventEnd && CalObjTimeCompare (cotime, eventEnd,
                                        CALOBJ_MONTH) > 0)
        return TRUE;
    if (intervalEnd && CalObjTimeCompare (cotime, intervalEnd,
                                           CALOBJ_MONTH) > 0)
        return TRUE;

    return FALSE;
}



static BOOL
CalObjWeeklyFindStartPosition (CalObjTime *eventStart,
                               CalObjTime *eventEnd,
                               RecurData  *recurData,
                               CalObjTime *intervalStart,
                               CalObjTime *intervalEnd,
                               CalObjTime *cotime)
{
    BongoCalTime eventStartDate, intervalStartDate;
    uint32_t eventStartJulian, intervalStartJulian;
    int intervalStartWeekdayOffset;
    CalObjTime weekStart;

    if (eventEnd && CalObjTimeCompare (eventEnd, intervalStart,
                                       CALOBJ_DAY) < 0) {
        DPRINT("event ends before interval starts\n");
        return TRUE;
    }
    
    if (intervalEnd && CalObjTimeCompare (eventStart, intervalEnd,
                                          CALOBJ_DAY) > 0) {
        DPRINT("interval ends before event starts\n");
        return TRUE;
    }

    *cotime = *eventStart;

    /* Convert the event start and interval start to BongoCalTimes, so we can
       easily find the number of days between them. */
    eventStartDate = BongoCalTimeNewDate(eventStart->day, eventStart->month, eventStart->year);
    intervalStartDate = BongoCalTimeNewDate(intervalStart->day, intervalStart->month, intervalStart->year);

    /* Calculate the start of the weeks corresponding to the event start
       and interval start. */
    eventStartJulian = BongoCalTimeGetJulianDay (eventStartDate);
    eventStartJulian -= recurData->weekdayOffset;

    intervalStartJulian = BongoCalTimeGetJulianDay (intervalStartDate);
    intervalStartWeekdayOffset = CalObjTimeWeekdayOffset (intervalStart, recurData->recur);
    intervalStartJulian -= intervalStartWeekdayOffset;

    /* We want to find the first full week using the recurrence interval
       that intersects the given interval dates. */
    if (eventStartJulian < intervalStartJulian) {
        int weeks = (intervalStartJulian - eventStartJulian) / 7;
        weeks += recurData->recur->interval - 1;
        weeks -= weeks % recurData->recur->interval;
        CalObjTimeAddDays (cotime, weeks * 7);
    }

    weekStart = *cotime;
    CalObjTimeAddDays (&weekStart, -recurData->weekdayOffset);

    if (eventEnd && CalObjTimeCompare (&weekStart, eventEnd,
                                       CALOBJ_DAY) > 0) {
        DPRINT("event ends before interval starts\n");
        return TRUE;
    }
    
    if (intervalEnd && CalObjTimeCompare (&weekStart, intervalEnd,
                                          CALOBJ_DAY) > 0) {
        DPRINT("interval ends before event starts\n");
        return TRUE;
    }

    DPRINT("weekly start position is %s\n", CalObjTimeToString(cotime));

    return FALSE;
}


static BOOL
CalObjWeeklyFindNextPosition (CalObjTime *cotime,
                              CalObjTime *eventEnd,
                              RecurData  *recurData,
                              CalObjTime *intervalEnd)
{
    CalObjTime weekStart;

    CalObjTimeAddDays (cotime, recurData->recur->interval * 7);

    /* Return TRUE if the start of this week is after the event finishes
       or is after the end of the required interval. */
    weekStart = *cotime;
    CalObjTimeAddDays (&weekStart, -recurData->weekdayOffset);

    DPRINT("Next  day: %s\n", CalObjTimeToString (cotime));
    DPRINT("Week Start: %s\n", CalObjTimeToString (&weekStart));
    DPRINT ("Interval end reached: %s\n", CalObjTimeToString (intervalEnd));

    if (eventEnd && CalObjTimeCompare (&weekStart, eventEnd,
                                       CALOBJ_DAY) > 0) {
        return TRUE;
    }
    
    if (intervalEnd && CalObjTimeCompare (&weekStart, intervalEnd,
                                           CALOBJ_DAY) > 0) {
        DPRINT ("Interval end reached: %s\n", CalObjTimeToString (intervalEnd));
        return TRUE;
    }

    return FALSE;
}


static BOOL
CalObjDailyFindStartPosition  (CalObjTime *eventStart,
                               CalObjTime *eventEnd,
                               RecurData  *recurData,
                               CalObjTime *intervalStart,
                               CalObjTime *intervalEnd,
                               CalObjTime *cotime)
{
    BongoCalTime eventStartDate, intervalStartDate;
    uint32_t eventStartJulian, intervalStartJulian, days;

    if (intervalEnd && CalObjTimeCompare (eventStart, intervalEnd,
                                           CALOBJ_DAY) > 0)
        return TRUE;
    if (eventEnd && CalObjTimeCompare (eventEnd, intervalStart,
                                        CALOBJ_DAY) < 0)
        return TRUE;

    *cotime = *eventStart;

    /* Convert the event start and interval start to BongoCalTime, so we can
       easily find the number of days between them. */
    eventStartDate = BongoCalTimeNewDate(eventStart->day, eventStart->month, eventStart->year);
    intervalStartDate = BongoCalTimeNewDate(intervalStart->day, intervalStart->month, intervalStart->year);
    eventStartJulian = BongoCalTimeGetJulianDay (eventStartDate);
    intervalStartJulian = BongoCalTimeGetJulianDay (intervalStartDate);

    if (eventStartJulian < intervalStartJulian) {
        days = intervalStartJulian - eventStartJulian
            + recurData->recur->interval - 1;
        days -= days % recurData->recur->interval;
        CalObjTimeAddDays (cotime, days);
    }

    if (eventEnd && CalObjTimeCompare (cotime, eventEnd,
                                        CALOBJ_DAY) > 0)
        return TRUE;
    if (intervalEnd && CalObjTimeCompare (cotime, intervalEnd,
                                           CALOBJ_DAY) > 0)
        return TRUE;

    return FALSE;
}


static BOOL
CalObjDailyFindNextPosition  (CalObjTime *cotime,
                              CalObjTime *eventEnd,
                              RecurData  *recurData,
                              CalObjTime *intervalEnd)
{
    CalObjTimeAddDays (cotime, recurData->recur->interval);

    if (eventEnd && CalObjTimeCompare (cotime, eventEnd,
                                        CALOBJ_DAY) > 0)
        return TRUE;
    if (intervalEnd && CalObjTimeCompare (cotime, intervalEnd,
                                           CALOBJ_DAY) > 0)
        return TRUE;

    return FALSE;
}


static BOOL
CalObjHourlyFindStartPosition (CalObjTime *eventStart,
                               CalObjTime *eventEnd,
                               RecurData  *recurData,
                               CalObjTime *intervalStart,
                               CalObjTime *intervalEnd,
                               CalObjTime *cotime)
{
    BongoCalTime eventStartDate, intervalStartDate;
    uint32_t eventStartJulian, intervalStartJulian, hours;

    if (intervalEnd && CalObjTimeCompare (eventStart, intervalEnd,
                                           CALOBJ_HOUR) > 0)
        return TRUE;
    if (eventEnd && CalObjTimeCompare (eventEnd, intervalStart,
                                        CALOBJ_HOUR) < 0)
        return TRUE;

    *cotime = *eventStart;

    if (CalObjTimeCompare (eventStart, intervalStart,
                           CALOBJ_HOUR) < 0) {
        /* Convert the event start and interval start to BongoCalTimes, so we
           can easily find the number of days between them. */
        eventStartDate = BongoCalTimeNewDate(eventStart->day, eventStart->month, eventStart->year);
        intervalStartDate = BongoCalTimeNewDate(intervalStart->day, intervalStart->month, intervalStart->year);
        eventStartJulian = BongoCalTimeGetJulianDay (eventStartDate);
        intervalStartJulian = BongoCalTimeGetJulianDay (intervalStartDate);

        hours = (intervalStartJulian - eventStartJulian) * 24;
        hours += intervalStart->hour - eventStart->hour;
        hours += recurData->recur->interval - 1;
        hours -= hours % recurData->recur->interval;
        CalObjTimeAddHours (cotime, hours);
    }

    if (eventEnd && CalObjTimeCompare (cotime, eventEnd,
                                        CALOBJ_HOUR) > 0)
        return TRUE;
    if (intervalEnd && CalObjTimeCompare (cotime, intervalEnd,
                                           CALOBJ_HOUR) > 0)
        return TRUE;

    return FALSE;
}


static BOOL
CalObjHourlyFindNextPosition (CalObjTime *cotime,
                              CalObjTime *eventEnd,
                              RecurData  *recurData,
                              CalObjTime *intervalEnd)
{
    CalObjTimeAddHours (cotime, recurData->recur->interval);

    if (eventEnd && CalObjTimeCompare (cotime, eventEnd,
                                        CALOBJ_HOUR) > 0)
        return TRUE;
    if (intervalEnd && CalObjTimeCompare (cotime, intervalEnd,
                                           CALOBJ_HOUR) > 0)
        return TRUE;

    return FALSE;
}


static BOOL
CalObjMinutelyFindStartPosition (CalObjTime *eventStart,
                                 CalObjTime *eventEnd,
                                 RecurData  *recurData,
                                 CalObjTime *intervalStart,
                                 CalObjTime *intervalEnd,
                                 CalObjTime *cotime)
{
    BongoCalTime eventStartDate, intervalStartDate;
    uint32_t eventStartJulian, intervalStartJulian, minutes;

    if (intervalEnd && CalObjTimeCompare (eventStart, intervalEnd,
                                           CALOBJ_MINUTE) > 0)
        return TRUE;
    if (eventEnd && CalObjTimeCompare (eventEnd, intervalStart,
                                        CALOBJ_MINUTE) < 0)
        return TRUE;

    *cotime = *eventStart;

    if (CalObjTimeCompare (eventStart, intervalStart,
                           CALOBJ_MINUTE) < 0) {
        /* Convert the event start and interval start to BongoCalTimes, so we
           can easily find the number of days between them. */
        eventStartDate = BongoCalTimeNewDate(eventStart->day, eventStart->month, eventStart->year);
        intervalStartDate = BongoCalTimeNewDate(intervalStart->day, intervalStart->month, intervalStart->year);
        eventStartJulian = BongoCalTimeGetJulianDay (eventStartDate);
        intervalStartJulian = BongoCalTimeGetJulianDay (intervalStartDate);

        minutes = (intervalStartJulian - eventStartJulian)
            * 24 * 60;
        minutes += (intervalStart->hour - eventStart->hour) * 24;
        minutes += intervalStart->minute - eventStart->minute;
        minutes += recurData->recur->interval - 1;
        minutes -= minutes % recurData->recur->interval;
        CalObjTimeAddMinutes (cotime, minutes);
    }

    if (eventEnd && CalObjTimeCompare (cotime, eventEnd,
                                        CALOBJ_MINUTE) > 0)
        return TRUE;
    if (intervalEnd && CalObjTimeCompare (cotime, intervalEnd,
                                           CALOBJ_MINUTE) > 0)
        return TRUE;

    return FALSE;
}


static BOOL
CalObjMinutelyFindNextPosition (CalObjTime *cotime,
                                CalObjTime *eventEnd,
                                RecurData  *recurData,
                                CalObjTime *intervalEnd)
{
    CalObjTimeAddMinutes (cotime, recurData->recur->interval);

    if (eventEnd && CalObjTimeCompare (cotime, eventEnd,
                                        CALOBJ_MINUTE) > 0)
        return TRUE;
    if (intervalEnd && CalObjTimeCompare (cotime, intervalEnd,
                                           CALOBJ_MINUTE) > 0)
        return TRUE;

    return FALSE;
}


static BOOL
CalObjSecondlyFindStartPosition (CalObjTime *eventStart,
                                 CalObjTime *eventEnd,
                                 RecurData  *recurData,
                                 CalObjTime *intervalStart,
                                 CalObjTime *intervalEnd,
                                 CalObjTime *cotime)
{
    BongoCalTime eventStartDate, intervalStartDate;
    uint32_t eventStartJulian, intervalStartJulian, seconds;

    if (intervalEnd && CalObjTimeCompare (eventStart, intervalEnd,
                                           CALOBJ_SECOND) > 0)
        return TRUE;
    if (eventEnd && CalObjTimeCompare (eventEnd, intervalStart,
                                        CALOBJ_SECOND) < 0)
        return TRUE;

    *cotime = *eventStart;

    if (CalObjTimeCompare (eventStart, intervalStart,
                           CALOBJ_SECOND) < 0) {
        /* Convert the event start and interval start to BongoDates, so we
           can easily find the number of days between them. */
        eventStartDate = BongoCalTimeNewDate(eventStart->day, eventStart->month, eventStart->year);
        intervalStartDate = BongoCalTimeNewDate(intervalStart->day, intervalStart->month, intervalStart->year);
        eventStartJulian = BongoCalTimeGetJulianDay (eventStartDate);
        intervalStartJulian = BongoCalTimeGetJulianDay (intervalStartDate);

        seconds = (intervalStartJulian - eventStartJulian)
            * 24 * 60 * 60;
        seconds += (intervalStart->hour - eventStart->hour)
            * 24 * 60;
        seconds += (intervalStart->minute - eventStart->minute) * 60;
        seconds += intervalStart->second - eventStart->second;
        seconds += recurData->recur->interval - 1;
        seconds -= seconds % recurData->recur->interval;
        CalObjTimeAddSeconds (cotime, seconds);
    }

    if (eventEnd && CalObjTimeCompare (cotime, eventEnd,
                                        CALOBJ_SECOND) >= 0)
        return TRUE;
    if (intervalEnd && CalObjTimeCompare (cotime, intervalEnd,
                                           CALOBJ_SECOND) >= 0)
        return TRUE;

    return FALSE;
}


static BOOL
CalObjSecondlyFindNextPosition (CalObjTime *cotime,
                                CalObjTime *eventEnd,
                                RecurData  *recurData,
                                CalObjTime *intervalEnd)
{
    CalObjTimeAddSeconds (cotime, recurData->recur->interval);

    if (eventEnd && CalObjTimeCompare (cotime, eventEnd,
                                        CALOBJ_SECOND) >= 0)
        return TRUE;
    if (intervalEnd && CalObjTimeCompare (cotime, intervalEnd,
                                           CALOBJ_SECOND) >= 0)
        return TRUE;

    return FALSE;
}





/* If the BYMONTH rule is specified it expands each occurrence in occs, by
   using each of the months in the bymonth list. */
static GArray*
CalObjByMonthExpand             (RecurData  *recurData,
                                 GArray     *occs)
{
    GArray *new_occs;
    CalObjTime *occ;
    BongoList *elem;
    int len, i;

    /* If BYMONTH has not been specified, or the array is empty, just
       return the array. */
    if (!recurData->recur->bymonth || occs->len == 0)
        return occs;

    new_occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));

    len = occs->len;
    for (i = 0; i < len; i++) {
        occ = &g_array_index(occs, CalObjTime, i);

        elem = recurData->recur->bymonth;
        while (elem) {
            /* NOTE: The day may now be invalid, e.g. 31st Feb. */
            occ->month = BongoListVoidToInt(elem->data);
            g_array_append_vals(new_occs, occ, 1);
            elem = elem->next;
        }
    }

    g_array_free(occs, TRUE);

    return new_occs;
}


/* If the BYMONTH rule is specified it filters out all occurrences in occs
   which do not match one of the months in the bymonth list. */
static GArray*
CalObjByMonthFilter             (RecurData  *recurData,
                                 GArray     *occs)
{
    GArray *new_occs;
    CalObjTime *occ;
    int len, i;

    /* If BYMONTH has not been specified, or the array is empty, just
       return the array. */
    if (!recurData->recur->bymonth || occs->len == 0)
        return occs;

    new_occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));

    len = occs->len;
    for (i = 0; i < len; i++) {
        occ = &g_array_index(occs, CalObjTime, i);
        if (recurData->months[occ->month])
            g_array_append_vals(new_occs, occ, 1);
    }

    g_array_free(occs, TRUE);

    return new_occs;
}



static GArray*
CalObjByWeeknoExpand            (RecurData  *recurData,
                                 GArray     *occs)
{
    GArray *new_occs;
    CalObjTime *occ, year_start_cotime, year_end_cotime, cotime;
    BongoList *elem;
    int len, i, weekno;

    /* If BYWEEKNO has not been specified, or the array is empty, just
       return the array. */
    if (!recurData->recur->byweekno || occs->len == 0)
        return occs;

    new_occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));

    len = occs->len;
    for (i = 0; i < len; i++) {
        occ = &g_array_index(occs, CalObjTime, i);

        /* Find the day that would correspond to week 1 (note that
           week 1 is the first week starting from the specified week
           start day that has 4 days in the new year). */
        year_start_cotime = *occ;
        CalObjFindFirstWeek (&year_start_cotime,
                             recurData);

        /* Find the day that would correspond to week 1 of the next
           year, which we use for -ve week numbers. */
        year_end_cotime = *occ;
        year_end_cotime.year++;
        CalObjFindFirstWeek (&year_end_cotime,
                             recurData);

        /* Now iterate over the week numbers in byweekno, generating a
           new occurrence for each one. */
        elem = recurData->recur->byweekno;
        while (elem) {
            weekno = BongoListVoidToInt(elem->data);
            if (weekno > 0) {
                cotime = year_start_cotime;
                CalObjTimeAddDays (&cotime,
                                   (weekno - 1) * 7);
            } else {
                cotime = year_end_cotime;
                CalObjTimeAddDays (&cotime, weekno * 7);
            }

            /* Skip occurrences if they fall outside the year. */
            if (cotime.year == occ->year)
                g_array_append_val(new_occs, cotime);
            elem = elem->next;
        }
    }

    g_array_free(occs, TRUE);

    return new_occs;
}


#if 0
/* This isn't used at present. */
static GArray*
CalObjByWeeknoFilter            (RecurData  *recurData,
                                 GArray     *occs)
{

    return occs;
}
#endif


static GArray*
CalObjByYeardayExpand   (RecurData  *recurData,
                         GArray     *occs)
{
    GArray *new_occs;
    CalObjTime *occ, year_start_cotime, year_end_cotime, cotime;
    BongoList *elem;
    int len, i, dayno;

    /* If BYYEARDAY has not been specified, or the array is empty, just
       return the array. */
    if (!recurData->recur->byyearday || occs->len == 0)
        return occs;

    new_occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));

    len = occs->len;
    for (i = 0; i < len; i++) {
        occ = &g_array_index(occs, CalObjTime, i);

        /* Find the day that would correspond to day 1. */
        year_start_cotime = *occ;
        year_start_cotime.month = 0;
        year_start_cotime.day = 1;

        /* Find the day that would correspond to day 1 of the next
           year, which we use for -ve day numbers. */
        year_end_cotime = *occ;
        year_end_cotime.year++;
        year_end_cotime.month = 0;
        year_end_cotime.day = 1;

        /* Now iterate over the day numbers in byyearday, generating a
           new occurrence for each one. */
        elem = recurData->recur->byyearday;
        while (elem) {
            dayno = BongoListVoidToInt(elem->data);
            if (dayno > 0) {
                cotime = year_start_cotime;
                CalObjTimeAddDays (&cotime, dayno - 1);
            } else {
                cotime = year_end_cotime;
                CalObjTimeAddDays (&cotime, dayno);
            }

            /* Skip occurrences if they fall outside the year. */
            if (cotime.year == occ->year)
                g_array_append_val(new_occs, cotime);
            elem = elem->next;
        }
    }

    g_array_free(occs, TRUE);

    return new_occs;
}


/* Note: occs must not contain invalid dates, e.g. 31st September. */
static GArray*
CalObjByYeardayFilter   (RecurData  *recurData,
                         GArray     *occs)
{
    GArray *new_occs;
    CalObjTime *occ;
    int yearday, len, i, days_in_year;

    /* If BYYEARDAY has not been specified, or the array is empty, just
       return the array. */
    if (!recurData->recur->byyearday || occs->len == 0)
        return occs;

    new_occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));

    len = occs->len;
    for (i = 0; i < len; i++) {
        occ = &g_array_index(occs, CalObjTime, i);
        yearday = CalObjDayOfYear (occ);
        if (recurData->yeardays[yearday]) {
            g_array_append_vals(new_occs, occ, 1);
        } else {
            days_in_year = BongoCalIsLeapYear (occ->year)
                ? 366 : 365;
            if (recurData->negYeardays[days_in_year + 1
                                         - yearday])
                g_array_append_vals(new_occs, occ, 1);
        }
    }

    g_array_free(occs, TRUE);

    return new_occs;
}



static GArray*
CalObjBymonthdayExpand  (RecurData  *recurData,
                         GArray     *occs)
{
    GArray *new_occs;
    CalObjTime *occ, month_start_cotime, month_end_cotime, cotime;
    BongoList *elem;
    int len, i, dayno;

    /* If BYMONTHDAY has not been specified, or the array is empty, just
       return the array. */
    if (!recurData->recur->bymonthday || occs->len == 0)
        return occs;

    new_occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));

    len = occs->len;
    for (i = 0; i < len; i++) {
        occ = &g_array_index(occs, CalObjTime, i);

        /* Find the day that would correspond to day 1. */
        month_start_cotime = *occ;
        month_start_cotime.day = 1;

        /* Find the day that would correspond to day 1 of the next
           month, which we use for -ve day numbers. */
        month_end_cotime = *occ;
        month_end_cotime.month++;
        month_end_cotime.day = 1;

        /* Now iterate over the day numbers in bymonthday, generating a
           new occurrence for each one. */
        elem = recurData->recur->bymonthday;
        while (elem) {
            dayno = BongoListVoidToInt(elem->data);
            if (dayno > 0) {
                cotime = month_start_cotime;
                CalObjTimeAddDays (&cotime, dayno - 1);
            } else {
                cotime = month_end_cotime;
                CalObjTimeAddDays (&cotime, dayno);
            }
            if (cotime.month == occ->month) {
                g_array_append_val(new_occs, cotime);
            } else {
                /* set to last day in month */
                cotime.month = occ->month;
                cotime.day = BongoCalDaysInMonth(occ->month, occ->year);
                g_array_append_val(new_occs, cotime);
            }
                                
            elem = elem->next;
        }
    }

    g_array_free(occs, TRUE);

    return new_occs;
}


static GArray*
CalObjBymonthdayFilter  (RecurData  *recurData,
                         GArray     *occs)
{
    GArray *new_occs;
    CalObjTime *occ;
    int len, i, daysInMonth;

    /* If BYMONTHDAY has not been specified, or the array is empty, just
       return the array. */
    if (!recurData->recur->bymonthday || occs->len == 0)
        return occs;

    new_occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));

    len = occs->len;
    for (i = 0; i < len; i++) {
        occ = &g_array_index(occs, CalObjTime, i);
        if (recurData->monthdays[occ->day]) {
            g_array_append_vals(new_occs, occ, 1);
        } else {
            daysInMonth = BongoCalDaysInMonth (occ->month, occ->year);
            if (recurData->negMonthdays[daysInMonth + 1 - occ->day])
                g_array_append_vals(new_occs, occ, 1);
        }
    }

    g_array_free(occs, TRUE);

    return new_occs;
}



static GArray*
CalObjByDayExpandYearly (RecurData  *recurData,
                         GArray     *occs)
{
    GArray *new_occs;
    CalObjTime *occ;
    BongoList *elem;
    int len, i, weekday, week_num;
    int first_weekday, last_weekday, offset;
    uint16_t year;

    /* If BYDAY has not been specified, or the array is empty, just
       return the array. */
    if (!recurData->recur->byday || occs->len == 0)
        return occs;

    new_occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));

    len = occs->len;
    for (i = 0; i < len; i++) {
        occ = &g_array_index(occs, CalObjTime, i);

        elem = recurData->recur->byday;
        while (elem) {
            weekday = BongoListVoidToInt(elem->data);
            elem = elem->next;
            week_num = BongoListVoidToInt(elem->data);
            elem = elem->next;

            year = occ->year;
            if (week_num == 0) {
                /* Expand to every Mon/Tue/etc. in the year. */
                occ->month = 0;
                occ->day = 1;
                first_weekday = CalObjTimeWeekday (occ);
                offset = (weekday + 7 - first_weekday) % 7;
                CalObjTimeAddDays (occ, offset);

                while (occ->year == year) {
                    g_array_append_vals(new_occs, occ, 1);
                    CalObjTimeAddDays (occ, 7);
                }

            } else if (week_num > 0) {
                /* Add the nth Mon/Tue/etc. in the year. */
                occ->month = 0;
                occ->day = 1;
                first_weekday = CalObjTimeWeekday (occ);
                offset = (weekday + 7 - first_weekday) % 7;
                offset += (week_num - 1) * 7;
                CalObjTimeAddDays (occ, offset);
                if (occ->year == year)
                    g_array_append_vals(new_occs, occ, 1);

            } else {
                /* Add the -nth Mon/Tue/etc. in the year. */
                occ->month = 11;
                occ->day = 31;
                last_weekday = CalObjTimeWeekday (occ);
                offset = (last_weekday + 7 - weekday) % 7;
                offset += (week_num - 1) * 7;
                CalObjTimeAddDays (occ, -offset);
                if (occ->year == year)
                    g_array_append_vals(new_occs, occ, 1);
            }

            /* Reset the year, as we may have gone past the end. */
            occ->year = year;
        }
    }

    g_array_free(occs, TRUE);

    return new_occs;
}


static GArray*
CalObjByDayExpandMonthly        (RecurData  *recurData,
                                 GArray     *occs)
{
    GArray *new_occs;
    CalObjTime *occ;
    BongoList *elem;
    int len, i, weekday, week_num;
    int first_weekday, last_weekday, offset;
    uint16_t year;
    uint8_t month;

    /* If BYDAY has not been specified, or the array is empty, just
       return the array. */
    if (!recurData->recur->byday || occs->len == 0)
        return occs;

    new_occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));

    len = occs->len;
    for (i = 0; i < len; i++) {
        occ = &g_array_index(occs, CalObjTime, i);

        elem = recurData->recur->byday;
        while (elem) {
            weekday = BongoListVoidToInt(elem->data);
            elem = elem->next;
            week_num = BongoListVoidToInt(elem->data);
            elem = elem->next;

            year = occ->year;
            month = occ->month;
            if (week_num == 0) {
                /* Expand to every Mon/Tue/etc. in the month.*/
                occ->day = 1;
                first_weekday = CalObjTimeWeekday (occ);
                offset = (weekday + 7 - first_weekday) % 7;
                CalObjTimeAddDays (occ, offset);

                while (occ->year == year
                       && occ->month == month) {
                    g_array_append_vals(new_occs, occ, 1);
                    CalObjTimeAddDays (occ, 7);
                }

            } else if (week_num > 0) {
                /* Add the nth Mon/Tue/etc. in the month. */
                occ->day = 1;
                first_weekday = CalObjTimeWeekday (occ);
                offset = (weekday + 7 - first_weekday) % 7;
                offset += (week_num - 1) * 7;
                CalObjTimeAddDays (occ, offset);
                if (occ->year == year && occ->month == month)
                    g_array_append_vals(new_occs, occ, 1);

            } else {
                /* Add the -nth Mon/Tue/etc. in the month. */
                occ->day = BongoCalDaysInMonth(occ->month, occ->year);
                last_weekday = CalObjTimeWeekday (occ);

                /* This calculates the number of days to step
                   backwards from the last day of the month
                   to the weekday we want. */
                offset = (last_weekday + 7 - weekday) % 7;

                /* This adds on the weeks. */
                offset += (-week_num - 1) * 7;

                CalObjTimeAddDays (occ, -offset);
                if (occ->year == year && occ->month == month)
                    g_array_append_vals(new_occs, occ, 1);
            }

            /* Reset the year & month, as we may have gone past
               the end. */
            occ->year = year;
            occ->month = month;
        }
    }

    g_array_free(occs, TRUE);

    return new_occs;
}


/* Note: occs must not contain invalid dates, e.g. 31st September. */
static GArray*
CalObjByDayExpandWeekly (RecurData  *recurData,
                         GArray     *occs)
{
    GArray *new_occs;
    CalObjTime *occ;
    BongoList *elem;
    int len, i, weekday, week_num;
    int weekdayOffset, new_weekdayOffset;

    /* If BYDAY has not been specified, or the array is empty, just
       return the array. */
    if (!recurData->recur->byday || occs->len == 0){
        DPRINT("no byday specified\n");
        return occs;
    }
    
    DPRINT("expanding byday\n");

    new_occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));

    len = occs->len;
    for (i = 0; i < len; i++) {
        occ = &g_array_index(occs, CalObjTime, i);

        elem = recurData->recur->byday;
        while (elem) {
            weekday = BongoListVoidToInt(elem->data);
            DPRINT("weekday is %d\n", weekday);
            
            elem = elem->next;

            /* FIXME: Currently we just ignore this, but maybe we
               should skip all elements where week_num != 0.
               The spec isn't clear about this. */
            week_num = BongoListVoidToInt(elem->data);
            DPRINT("week num is %d\n", week_num);

            elem = elem->next;

            weekdayOffset = CalObjTimeWeekdayOffset (occ, recurData->recur);
            DPRINT("weekday offset: %d\n", weekdayOffset);
            
            new_weekdayOffset = (weekday + 7 - recurData->recur->weekStartDay) % 7;
            DPRINT("new weekday offset: %d\n", weekdayOffset);

            CalObjTimeAddDays (occ, new_weekdayOffset - weekdayOffset);

            DPRINT("appending a value\n");
            g_array_append_vals(new_occs, occ, 1);
        }
    }

    g_array_free(occs, TRUE);

    return new_occs;
}


/* Note: occs must not contain invalid dates, e.g. 31st September. */
static GArray*
CalObjBydayFilter               (RecurData  *recurData,
                                 GArray     *occs)
{
    GArray *new_occs;
    CalObjTime *occ;
    int len, i, weekday;

    /* If BYDAY has not been specified, or the array is empty, just
       return the array. */
    if (!recurData->recur->byday || occs->len == 0)
        return occs;

    new_occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));

    len = occs->len;
    for (i = 0; i < len; i++) {
        occ = &g_array_index(occs, CalObjTime, i);
        weekday = CalObjTimeWeekday (occ);

        /* See if the weekday on its own is set. */
        if (recurData->weekdays[weekday])
            g_array_append_vals(new_occs, occ, 1);
    }

    g_array_free(occs, TRUE);

    return new_occs;
}



/* If the BYHOUR rule is specified it expands each occurrence in occs, by
   using each of the hours in the byhour list. */
static GArray*
CalObjByhourExpand              (RecurData  *recurData,
                                 GArray     *occs)
{
    GArray *new_occs;
    CalObjTime *occ;
    BongoList *elem;
    int len, i;

    /* If BYHOUR has not been specified, or the array is empty, just
       return the array. */
    if (!recurData->recur->byhour || occs->len == 0)
        return occs;

    new_occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));

    len = occs->len;
    for (i = 0; i < len; i++) {
        occ = &g_array_index(occs, CalObjTime, i);

        elem = recurData->recur->byhour;
        while (elem) {
            occ->hour = BongoListVoidToInt(elem->data);
            g_array_append_vals(new_occs, occ, 1);
            elem = elem->next;
        }
    }

    g_array_free(occs, TRUE);

    return new_occs;
}


/* If the BYHOUR rule is specified it filters out all occurrences in occs
   which do not match one of the hours in the byhour list. */
static GArray*
CalObjByhourFilter              (RecurData  *recurData,
                                 GArray     *occs)
{
    GArray *new_occs;
    CalObjTime *occ;
    int len, i;

    /* If BYHOUR has not been specified, or the array is empty, just
       return the array. */
    if (!recurData->recur->byhour || occs->len == 0)
        return occs;

    new_occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));

    len = occs->len;
    for (i = 0; i < len; i++) {
        occ = &g_array_index(occs, CalObjTime, i);
        if (recurData->hours[occ->hour])
            g_array_append_vals(new_occs, occ, 1);
    }

    g_array_free(occs, TRUE);

    return new_occs;
}



/* If the BYMINUTE rule is specified it expands each occurrence in occs, by
   using each of the minutes in the byminute list. */
static GArray*
CalObjByminuteExpand            (RecurData  *recurData,
                                 GArray     *occs)
{
    GArray *new_occs;
    CalObjTime *occ;
    BongoList *elem;
    int len, i;

    /* If BYMINUTE has not been specified, or the array is empty, just
       return the array. */
    if (!recurData->recur->byminute || occs->len == 0)
        return occs;

    new_occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));

    len = occs->len;
    for (i = 0; i < len; i++) {
        occ = &g_array_index(occs, CalObjTime, i);

        elem = recurData->recur->byminute;
        while (elem) {
            occ->minute = BongoListVoidToInt(elem->data);
            g_array_append_vals(new_occs, occ, 1);
            elem = elem->next;
        }
    }

    g_array_free(occs, TRUE);

    return new_occs;
}


/* If the BYMINUTE rule is specified it filters out all occurrences in occs
   which do not match one of the minutes in the byminute list. */
static GArray*
CalObjByminuteFilter            (RecurData  *recurData,
                                 GArray     *occs)
{
    GArray *new_occs;
    CalObjTime *occ;
    int len, i;

    /* If BYMINUTE has not been specified, or the array is empty, just
       return the array. */
    if (!recurData->recur->byminute || occs->len == 0)
        return occs;

    new_occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));

    len = occs->len;
    for (i = 0; i < len; i++) {
        occ = &g_array_index(occs, CalObjTime, i);
        if (recurData->minutes[occ->minute])
            g_array_append_vals(new_occs, occ, 1);
    }

    g_array_free(occs, TRUE);

    return new_occs;
}



/* If the BYSECOND rule is specified it expands each occurrence in occs, by
   using each of the seconds in the bysecond list. */
static GArray*
CalObjBysecondExpand            (RecurData  *recurData,
                                 GArray     *occs)
{
    GArray *new_occs;
    CalObjTime *occ;
    BongoList *elem;
    int len, i;

    /* If BYSECOND has not been specified, or the array is empty, just
       return the array. */
    if (!recurData->recur->bysecond || occs->len == 0)
        return occs;

    new_occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));

    len = occs->len;
    for (i = 0; i < len; i++) {
        occ = &g_array_index(occs, CalObjTime, i);

        elem = recurData->recur->bysecond;
        while (elem) {
            occ->second = BongoListVoidToInt(elem->data);
            g_array_append_vals(new_occs, occ, 1);
            elem = elem->next;
        }
    }

    g_array_free(occs, TRUE);

    return new_occs;
}


/* If the BYSECOND rule is specified it filters out all occurrences in occs
   which do not match one of the seconds in the bysecond list. */
static GArray*
CalObjBysecondFilter            (RecurData  *recurData,
                                 GArray     *occs)
{
    GArray *new_occs;
    CalObjTime *occ;
    int len, i;

    /* If BYSECOND has not been specified, or the array is empty, just
       return the array. */
    if (!recurData->recur->bysecond || occs->len == 0)
        return occs;

    new_occs = g_array_new(FALSE, FALSE, sizeof(CalObjTime));

    len = occs->len;
    for (i = 0; i < len; i++) {
        occ = &g_array_index(occs, CalObjTime, i);
        if (recurData->seconds[occ->second])
            g_array_append_vals(new_occs, occ, 1);
    }

    g_array_free(occs, TRUE);

    return new_occs;
}





/* Adds a positive or negative number of months to the given CalObjTime,
   updating the year appropriately so we end up with a valid month.
   Note that the day may be invalid, e.g. 30th Feb. */
static void
CalObjTimeAddMonths             (CalObjTime *cotime,
                                 int         months)
{
    unsigned int month, years;

    /* We use a unsigned int to avoid overflow on the uint8_t. */
    month = cotime->month + months;
    cotime->month = month % 12;
    if (month > 0) {
        cotime->year += month / 12;
    } else {
        years = month / 12;
        if (cotime->month != 0) {
            cotime->month += 12;
            years -= 1;
        }
        cotime->year += years;
    }
}

static void
CalObjTimeAdjust(CalObjTime *cotime, int days, int hours, int minutes, int seconds)
{
    BongoCalTime t;
#if DEBUG_RECUR
    char buffer[1024];
#endif
    t = BongoCalTimeEmpty();
    
    t.year = cotime->year;
    t.month = cotime->month;
    t.day = cotime->day;
    t.hour = cotime->hour;
    t.minute = cotime->minute;
    t.second = cotime->second;
    t = BongoCalTimeSetTimezone(t, BongoCalTimezoneGetUtc());
    t.isDate = FALSE;
    
#if DEBUG_RECUR    
    BongoCalTimeToIcal(t, buffer, 1024);
    DPRINT("before adding %d days: %s\n", days, buffer);
#endif

    t = BongoCalTimeAdjust(t, days, hours, minutes, seconds);

#if DEBUG_RECUR    
    BongoCalTimeToIcal(t, buffer, 1024);
    DPRINT("after adjust: %s\n", buffer);
#endif
    
    cotime->year = t.year;
    cotime->month = t.month;
    cotime->day = t.day;
    cotime->hour = t.hour;
    cotime->minute = t.minute;
    cotime->second = t.second;
}

/* Adds a positive or negative number of days to the given CalObjTime,
   updating the month and year appropriately so we end up with a valid day. */
static void
CalObjTimeAddDays               (CalObjTime *cotime,
                                 int         days)
{
    CalObjTimeAdjust(cotime, days, 0, 0, 0);
}


/* Adds a positive or negative number of hours to the given CalObjTime,
   updating the day, month & year appropriately so we end up with a valid
   time. */
static void
CalObjTimeAddHours              (CalObjTime *cotime,
                                 int         hours)
{
    CalObjTimeAdjust(cotime, 0, hours, 0, 0);
}


/* Adds a positive or negative number of minutes to the given CalObjTime,
   updating the rest of the CalObjTime appropriately. */
static void
CalObjTimeAddMinutes    (CalObjTime *cotime,
                         int         minutes)
{
    CalObjTimeAdjust(cotime, 0, 0, minutes, 0);
}

/* Adds a positive or negative number of seconds to the given CalObjTime,
   updating the rest of the CalObjTime appropriately. */
static void
CalObjTimeAddSeconds    (CalObjTime *cotime,
                         int         seconds)
{
    CalObjTimeAdjust(cotime, 0, 0, 0, seconds);
}


/* Compares 2 CalObjTimes. Returns -1 if the cotime1 is before cotime2, 0 if
   they are the same, or 1 if cotime1 is after cotime2. The comparison type
   specifies which parts of the times we are interested in, e.g. if CALOBJ_DAY
   is used we only want to know if the days are different. */
static int
CalObjTimeCompare               (CalObjTime *cotime1,
                                 CalObjTime *cotime2,
                                 CalObjTimeComparison type)
{
    if (cotime1->year < cotime2->year)
        return -1;
    if (cotime1->year > cotime2->year)
        return 1;

    if (type == CALOBJ_YEAR)
        return 0;

    if (cotime1->month < cotime2->month)
        return -1;
    if (cotime1->month > cotime2->month)
        return 1;

    if (type == CALOBJ_MONTH)
        return 0;

    if (cotime1->day < cotime2->day)
        return -1;
    if (cotime1->day > cotime2->day)
        return 1;

    if (type == CALOBJ_DAY)
        return 0;

    if (cotime1->hour < cotime2->hour)
        return -1;
    if (cotime1->hour > cotime2->hour)
        return 1;

    if (type == CALOBJ_HOUR)
        return 0;

    if (cotime1->minute < cotime2->minute)
        return -1;
    if (cotime1->minute > cotime2->minute)
        return 1;

    if (type == CALOBJ_MINUTE)
        return 0;

    if (cotime1->second < cotime2->second)
        return -1;
    if (cotime1->second > cotime2->second)
        return 1;

    return 0;
}


/* This is the same as the above function, but without the comparison type.
   It is used for qsort(). */
static int
CalObjCompareFunc (const void *arg1,
                   const void *arg2)
{
    CalObjTime *cotime1, *cotime2;
    int retval;

    cotime1 = (CalObjTime*) arg1;
    cotime2 = (CalObjTime*) arg2;

    if (cotime1->year < cotime2->year)
        retval = -1;
    else if (cotime1->year > cotime2->year)
        retval = 1;

    else if (cotime1->month < cotime2->month)
        retval = -1;
    else if (cotime1->month > cotime2->month)
        retval = 1;

    else if (cotime1->day < cotime2->day)
        retval = -1;
    else if (cotime1->day > cotime2->day)
        retval = 1;

    else if (cotime1->hour < cotime2->hour)
        retval = -1;
    else if (cotime1->hour > cotime2->hour)
        retval = 1;

    else if (cotime1->minute < cotime2->minute)
        retval = -1;
    else if (cotime1->minute > cotime2->minute)
        retval = 1;

    else if (cotime1->second < cotime2->second)
        retval = -1;
    else if (cotime1->second > cotime2->second)
        retval = 1;

    else
        retval = 0;

#if 0
    g_print ("%s - ", CalObjTimeToString (cotime1));
    g_print ("%s : %i\n", CalObjTimeToString (cotime2), retval);
#endif

    return retval;
}


static int
CalObjDateOnlyCompareFunc (const void *arg1,
                           const void *arg2)
{
    CalObjTime *cotime1, *cotime2;

    cotime1 = (CalObjTime*) arg1;
    cotime2 = (CalObjTime*) arg2;

    if (cotime1->year < cotime2->year)
        return -1;
    if (cotime1->year > cotime2->year)
        return 1;

    if (cotime1->month < cotime2->month)
        return -1;
    if (cotime1->month > cotime2->month)
        return 1;

    if (cotime1->day < cotime2->day)
        return -1;
    if (cotime1->day > cotime2->day)
        return 1;

    return 0;
}


/* Returns the weekday of the given CalObjTime, from 0 (Mon) - 6 (Sun). */
static int
CalObjTimeWeekday               (CalObjTime *cotime)
{
    BongoCalTime date;
    int weekday;

    date = BongoCalTimeNewDate(cotime->day, cotime->month, cotime->year);
    /* This results in a value of 0 (Monday) - 6 (Sunday). */
    weekday = BongoCalTimeGetWeekday(date) - 1;

    return weekday;
}


/* Returns the weekday of the given CalObjTime, from 0 - 6. The week start
   day is Monday by default, but can be set in the recurrence rule. */
static int
CalObjTimeWeekdayOffset        (CalObjTime *cotime,
                                 BongoCalRule *recur)
{
    BongoCalTime date;
    int weekday, offset;

    date = BongoCalTimeNewDate(cotime->day, cotime->month, cotime->year);
    /* This results in a value of 0 (Monday) - 6 (Sunday). */
    weekday = BongoCalTimeGetWeekday(date) - 1;

    /* This calculates the offset of our day from the start of the week.
       We just add on a week (to avoid any possible negative values) and
       then subtract the specified week start day, then convert it into a
       value from 0-6. */
    offset = (weekday + 7 - recur->weekStartDay) % 7;

    return offset;
}


/* Returns the day of the year of the given CalObjTime, from 1 - 366. */
static int
CalObjDayOfYear         (CalObjTime *cotime)
{
    BongoCalTime date;
    
    date = BongoCalTimeNewDate(cotime->day, cotime->month, cotime->year);
    /* This results in a value of 0 (Monday) - 6 (Sunday). */
    return BongoCalTimeGetDayOfYear(date) - 1;
}


/* Finds the first week in the given CalObjTime's year, using the same weekday
   as the event start day (i.e. from the RecurData).
   The first week of the year is the first week starting from the specified
   week start day that has 4 days in the new year. It may be in the previous
   year. */
static void
CalObjFindFirstWeek     (CalObjTime *cotime,
                         RecurData  *recurData)
{
    BongoCalTime date;
    int weekday, weekStartDay, firstFullWeekStartOffset, offset;

    date = BongoCalTimeNewDate(cotime->day, cotime->month, cotime->year);
    /* This results in a value of 0 (Monday) - 6 (Sunday). */
    weekday = BongoCalTimeGetWeekday(date) - 1;

    /* Calculate the first day of the year that starts a new week, i.e. the
       first weekStartDay after weekday, using 0 = 1st Jan.
       e.g. if the 1st Jan is a Tuesday (1) and weekStartDay is a
       Monday (0), the result will be (0 + 7 - 1) % 7 = 6 (7th Jan). */
    weekStartDay = recurData->recur->weekStartDay;
    firstFullWeekStartOffset = (weekStartDay + 7 - weekday) % 7;

    /* Now see if we have to move backwards 1 week, i.e. if the week
       starts on or after Jan 5th (since the previous week has 4 days in
       this year and so will be the first week of the year). */
    if (firstFullWeekStartOffset >= 4)
        firstFullWeekStartOffset -= 7;

    /* Now add the days to get to the event's weekday. */
    offset = firstFullWeekStartOffset + recurData->weekdayOffset;

    /* Now move the cotime to the appropriate day. */
    cotime->month = 0;
    cotime->day = 1;
    CalObjTimeAddDays (cotime, offset);
}

static void
CalObjTimeFromTime      (CalObjTime     *cotime,
                         uint64_t t,
                         BongoCalTimezone *zone)
{
    BongoCalTime tt;

    tt = BongoCalTimeNewFromUint64(t, FALSE, zone);

    cotime->year     = tt.year;
    cotime->month    = tt.month;
    cotime->day      = tt.day;
    cotime->hour     = tt.hour;
    cotime->minute   = tt.minute;
    cotime->second   = tt.second;
    cotime->flags    = FALSE;
}


/* Debugging function to convert a CalObjTime to a string. It uses a static
   buffer so beware. */
#if DEBUG_RECUR
static char*
CalObjTimeToString              (CalObjTime     *cotime)
{
    static char buffer[20];
    char *weekdays[] = { "Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun",
                         "   " };
    int weekday;

    weekday = CalObjTimeWeekday (cotime);

    sprintf (buffer, "%s %02i/%02i/%04i %02i:%02i:%02i",
             weekdays[weekday],
             cotime->day, cotime->month + 1, cotime->year,
             cotime->hour, cotime->minute, cotime->second);
    return buffer;
}

static char*
BongoCalTimeToString              (BongoCalTime t)
{
    static char buffer[20];

    sprintf (buffer, "%02i/%02i/%04i %02i:%02i:%02i",
             t.day, t.month, t.year,
             t.hour, t.minute, t.second);
    return buffer;
}
#endif


/* This recalculates the end dates for recurrence & exception rules which use
   the COUNT property. If refresh is TRUE it will recalculate all enddates
   for rules which use COUNT. If refresh is FALSE, it will only calculate
   the enddate if it hasn't already been set. It returns TRUE if the component
   was changed, i.e. if the component should be saved at some point.
   We store the enddate in the "x-bongo-until" property of the RRULE
   or EXRULE. */
BOOL
BongoCalRecurEnsureEndDates (BongoCalInstance    *comp,
                            BOOL                 refresh,
                            BongoCalRecurResolveTimezoneFn  tzCb,
                            void *               tzCbData)
{
    BongoSList *rrules, *exrules, *elem;
    BOOL changed = FALSE;

    /* Do the RRULEs. */
    BongoCalInstanceGetRRules (comp, &rrules);
    for (elem = rrules; elem; elem = elem->next) {
        changed |= BongoCalRecurEnsureRuleEndDate (comp, elem->data,
                                                  FALSE, refresh,
                                                  tzCb, tzCbData);
    }

    /* Do the EXRULEs. */
    BongoCalInstanceGetExRules (comp, &exrules);
    for (elem = exrules; elem; elem = elem->next) {
        changed |= BongoCalRecurEnsureRuleEndDate (comp, elem->data,
                                                  TRUE, refresh,
                                                  tzCb, tzCbData);
    }

    return changed;
}


typedef struct _BongoCalRecurEnsureEndDateData BongoCalRecurEnsureEndDateData;
struct _BongoCalRecurEnsureEndDateData {
    int count;
    int instances;
    uint64_t endDate;
};


static BOOL
BongoCalRecurEnsureRuleEndDate (BongoCalInstance *comp,
                               BongoCalRule *recur,
                               BOOL exception,
                               BOOL refresh,
                               BongoCalRecurResolveTimezoneFn tzCb,
                               void *tzCbData)
{
    BongoCalRecurEnsureEndDateData cbData;

    UNUSED_PARAMETER_REFACTOR(exception)

    /* If the rule doesn't use COUNT just return. */
    if (recur->count == 0) {
        return FALSE;
    }

    /* If refresh is FALSE, we check if the enddate is already set, and
       if it is we just return. */
    if (!refresh) {
        if (recur->until != 0) {
            return FALSE;
        }
    }

    /* Calculate the end date. Note that we initialize endDate to 0, so
       if the RULE doesn't generate COUNT instances we save a time_t of 0.
       Also note that we use the UTC timezone as the default timezone.
       In get_end_date() if the DTSTART is a DATE or floating time, we will
       convert the ENDDATE to the current timezone. */
    cbData.count = recur->count;
    cbData.instances = 0;
    cbData.endDate = 0;
    BongoCalRecurGenerateInstancesOfRule (comp, recur, -1, -1,
                                         BongoCalRecurEnsureRuleEndDateCb,
                                         &cbData, tzCb, tzCbData,
                                         BongoCalTimezoneGetUtc());

    /* Store the end date in the "x-bongo-until" parameter of the
       rule. */
    BongoCalRecurSetRuleEndDate (recur, cbData.endDate);
                
    return TRUE;
}

static BOOL
BongoCalRecurEnsureRuleEndDateCb (BongoCalInstance *inst,
                                 uint64_t instanceStart,
                                 uint64_t instanceEnd,
                                 void *data)
{
    BongoCalRecurEnsureEndDateData *cbData;

    // these parameters are required by the prototype for the callback
    // if we want to remove them, we need to refactor the callback
    UNUSED_PARAMETER(inst)
    UNUSED_PARAMETER(instanceEnd)

    cbData = (BongoCalRecurEnsureEndDateData*) data;

    printf("GOT INSTANCE %d\n", cbData->instances);

    cbData->instances++;

    if (cbData->instances > cbData->count) {
        cbData->endDate = instanceStart - 1;
        return FALSE;
    }

    return TRUE;
}

static void
BongoCalRecurSetRuleEndDate(BongoCalRule *recur, uint64_t endDate)
{
    recur->until = endDate;

    if (recur->json) {
        BongoCalTime t;
        BongoJsonObject *params;
        char buffer[BONGO_CAL_TIME_BUFSIZE];
        t = BongoCalTimeNewFromUint64(endDate, FALSE, NULL);
        BongoCalTimeToIcal(t, buffer, sizeof(buffer));
        printf("ensuring an end date of %s\n", buffer);

        if (BongoJsonObjectGetObject(recur->json, "params", &params) != BONGO_JSON_OK) {
            params = BongoJsonObjectNew();
            BongoJsonObjectPutObject(recur->json, "params", params);
        }
            
        BongoJsonObjectPutString(params, "x-bongo-until", buffer);
        recur->until = endDate;
    } else {
        printf("no json, fishy\n");
    }
}
