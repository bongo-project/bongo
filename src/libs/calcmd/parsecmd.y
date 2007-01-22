/****************************************************************************
 * <Novell-copyright> * Copyright (c) 2005, 2006 Novell, Inc. All Rights Reserved.
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
%{
#include <config.h>
#include <xpl.h>
#include <memmgr.h>
#include <stdlib.h>
#include <stdio.h>
#include <ctype.h>
#include <bongocal.h>
#include <calcmd.h>
#include <setjmp.h>
    

static int VerboseMode;
static uint64_t BaseTime;


enum skip_type {
    SKIP_NONE =   0,
    SKIP_DAY =    1,
    SKIP_WEEK =   2,
    SKIP_MONTH =  3,
    SKIP_YEAR =   4,
};

struct daterange {      /*  convention: data in this struct can be set to  */
    BongoCalTime begin;    /*  -1 to indicate unknown or unspecified value    */
    BongoCalTime end;
    enum skip_type skip;
};

struct strparser {
    /* input */
    char *str;

    /* result */
    struct daterange *date;
    char *desc;

    /* internal */
    struct daterange *defaults;
    int idx;
    char *dptr;
    void *mempool;
    jmp_buf bailenv;
};

static int
init_strparser (struct strparser *p, char *str)
{    
    p->str = str;
    p->idx = 0;
    p->defaults = NULL;
    p->date = NULL;
    p->desc = MemMalloc(strlen(str) + 1);
    if (!p->desc) {
        return 0;
    }
    strcpy(p->desc, str);
    p->desc[0] = 0;
    p->dptr = p->desc;
    p->mempool = 
        MemPrivatePoolAlloc("Calendar Command Parser Pool", 
                            sizeof(struct daterange), 0, 100, TRUE, TRUE, 
                            NULL, NULL, NULL);
    if (!p->mempool) {
        MemFree(p->desc);
        return 0;
    }
    return 1;
}


static void
destroy_strparser (struct strparser *p)
{
    MemFree(p->desc);
    MemPrivatePoolFree(p->mempool);
}


static void
dump_daterange(struct daterange *d)
{
    if (!VerboseMode) {
        return;
    }
    fprintf (stderr, "%4d-%02d-%02d %02d:%02d to %4d-%02d-%02d %02d:%02d %s\n",
             d->begin.year, d->begin.month + 1, d->begin.day, 
             d->begin.hour, d->begin.minute,
             d->end.year, d->end.month + 1, d->end.day, 
             d->end.hour, d->end.minute,
             (SKIP_NONE == d->skip ? "" : 
              SKIP_DAY == d->skip ? "SKIP_DAY" :
              SKIP_WEEK == d->skip ? "SKIP_WEEK" :
              SKIP_MONTH == d->skip ? "SKIP_MONTH" :
              SKIP_YEAR == d->skip ? "SKIP_YEAR" :
              "SKIP_INVALID!!"));
}


static BongoCalTime *
skiptm (BongoCalTime *dt, enum skip_type skip, int amount)
{
    switch (skip) {
    case SKIP_NONE:
        break;
    case SKIP_DAY:
        dt->day += amount;
        break;
    case SKIP_WEEK:
        dt->day += 7 * amount;
        break;
    case SKIP_MONTH:
        dt->month += amount;
        break;
    case SKIP_YEAR:
        dt->year += amount;
        break;
    }
    *dt = BongoCalTimeAdjust(*dt, 0, 0, 0, 0);
    return dt;
}


static struct daterange *
new_daterange (struct strparser *p)
{
    struct daterange *result = MemPrivatePoolGetEntry(p->mempool);
    if (!result) {
        longjmp (p->bailenv, 1);
    }
    return result;
}


static struct daterange *
daterange_skip (struct daterange *d, int amount)
{
    skiptm (&d->begin, d->skip, amount);
    skiptm (&d->end, d->skip, amount);
    return d;
}


static struct daterange *
new_daterange_from_tms (struct strparser *p, BongoCalTime *begin, BongoCalTime *end)
{
    struct daterange *result = new_daterange(p);

    memcpy(&result->begin, begin, sizeof(BongoCalTime));
    memcpy(&result->end, end, sizeof(BongoCalTime));
    result->skip = SKIP_NONE;
    return result;
}

static struct daterange *
new_daterange_from_dateranges (struct strparser *p, 
                               struct daterange *begin, struct daterange *end)
{
    struct daterange *result = new_daterange(p);;
    uint64_t begin_ts = BongoCalTimeAsUint64 (begin->begin);
    uint64_t end_ts = BongoCalTimeAsUint64 (end->end);

    memcpy(&result->begin, &begin->begin, sizeof(BongoCalTime));
    memcpy(&result->end, &end->end, sizeof(BongoCalTime));

    if (BongoCalTimeGetDayOfYear(result->begin) == BongoCalTimeGetDayOfYear(result->end)) {
        result->end.hour = end->begin.hour;
        result->end.minute = end->begin.minute;
    }

    if (end_ts < begin_ts && end->skip) {
        skiptm (&result->end, end->skip, 1);
    } 

    result->skip = SKIP_NONE;

    dump_daterange(result);

    return result;
}


static struct daterange *
new_daterange_from_timeranges (struct strparser *p,
                               struct daterange *begin, struct daterange *end)
{
    struct daterange *result = new_daterange(p);;
    uint64_t begin_ts = BongoCalTimeAsUint64 (begin->begin);
    uint64_t end_ts = BongoCalTimeAsUint64 (end->end);

    memcpy(&result->begin, &begin->begin, sizeof(BongoCalTime));
    memcpy(&result->end, &end->begin, sizeof(BongoCalTime));

    if (end_ts < begin_ts && end->skip) {
        skiptm (&result->end, end->skip, 1);
    } 

    result->skip = SKIP_NONE;

    dump_daterange(result);

    return result;
}



static struct daterange *
new_date (struct strparser *p, struct daterange *defaults, 
          int year, int month, int day)
{
    struct daterange *result = new_daterange(p);;

    memcpy(&result->begin, &defaults->begin, sizeof(BongoCalTime));
    memcpy(&result->end, &defaults->end, sizeof(BongoCalTime));

    if (-1 != year) {
        result->begin.year = year;
    }
    if (-1 != month) {
        if (-1 == year) {
            if (month - defaults->begin.month < 0) {
                month += 12;
            }
        }
        result->begin.month = month;
    }
    if (-1 != day)   result->begin.day = day;
    result->begin.hour = 0;
    result->begin.minute = 0;
    result->begin.second = 0;
#if 0
    result->begin.tm_isdst = -1;
#endif

    result->end.year = result->begin.year;
    result->end.month =  result->begin.month;
    result->end.day = result->begin.day;
    result->end.hour = 23;
    result->end.minute = 59;
    result->end.second = 0;
#if 0
    result->begin.tm_isdst = -1;
#endif
    
    result->skip = SKIP_DAY;

    result->begin = BongoCalTimeAdjust(result->begin, 0, 0, 0, 0);
    result->end = BongoCalTimeAdjust(result->end, 0, 0, 0, 0);
    
    dump_daterange(result);

    return result;
}


static struct daterange *
new_dayofweek (struct strparser *p, struct daterange *defaults, int wday)
{
    struct daterange *result = new_daterange(p);;
    int d;

    memcpy(&result->begin, &defaults->begin, sizeof(BongoCalTime));

    d = wday - BongoCalTimeGetWeekday(result->begin);
    if (d < 0) {
        d += 7;
    }
    result->begin.day += d;
    result->begin = BongoCalTimeAdjust(result->begin, 0, 0, 0, 0);

    result->begin.hour = 0;
    result->begin.minute = 0;
    result->begin.second = 0;
#if 0
    result->begin.tm_isdst = -1;
#endif

    memcpy(&result->end, &result->begin, sizeof(BongoCalTime));

    result->end.hour = 23;
    result->end.minute = 59;
    result->end.second = 0;
#if 0
    result->begin.tm_isdst = -1;
#endif

    result->skip = SKIP_WEEK;

    dump_daterange(result);
    return result;
}


static struct daterange *
new_month (struct strparser *p, struct daterange *defaults, int mon, int year)
{
    struct daterange *result = new_daterange(p);;
    int d;

    memcpy(&result->begin, &defaults->begin, sizeof(BongoCalTime));

    if (-1 == mon) {
        result->skip = SKIP_MONTH;
        mon = result->begin.month;
    } else {
        result->skip = SKIP_YEAR;
    }

    d = mon - result->begin.month;
    if (d < 0) {
        d = d + 12;
    }
    result->begin.month += d;
    result->begin = BongoCalTimeAdjust(result->begin, 0, 0, 0, 0);

    result->begin.day = 1;
    result->begin.hour = 0;
    result->begin.minute = 0;
    result->begin.second = 0;
#if 0
    result->begin.tm_isdst = -1;
#endif
    if (-1 != year) {
        result->begin.year = year;
    }

    memcpy(&result->end, &result->begin, sizeof(BongoCalTime));

    result->end.month = result->begin.month + 1;
    result->end.day = 0;
    result->end.hour = 23;
    result->end.minute = 59;
    result->end.second = 0;
#if 0
    result->end.tm_isdst = -1;
#endif
    result->end = BongoCalTimeAdjust(result->end, 0, 0, 0, 0);

    dump_daterange(result);
    return result;
}


static struct daterange *
new_day_of_month (struct strparser *p, struct daterange *defaults, int day)
{
    struct daterange *result = new_date(p, defaults, -1, -1, day);;

    if (day < defaults->begin.day) {
        skiptm (&result->begin, SKIP_MONTH, 1);
        skiptm (&result->end, SKIP_MONTH, 1);
    } 
    return result;
}


/* pm: 0 for AM, 1 for PM, -1 for unknown */
static struct daterange *
new_time (struct strparser *p, struct daterange *defaults, 
          int hour, int min, int pm)
{
    struct daterange *result = new_daterange(p);;

    if (-1 == pm) { /* guess if they mean pm*/
        pm = hour < 7 ? 1 : 0;
    }

    if (pm && hour < 12) {
        hour += 12;
    }

    memcpy(&result->begin, &defaults->begin, sizeof(BongoCalTime));
    memcpy(&result->end, &defaults->end, sizeof(BongoCalTime));

    result->begin.hour = hour;
    result->begin.minute = min;
    result->begin.second = 0;
#if 0
    result->begin.tm_isdst = -1;
#endif

    result->end.hour = hour;
    result->end.minute = min + 59;
    result->end.second = 0;
#if 0
    result->end.tm_isdst = -1;
#endif

    result->end = BongoCalTimeAdjust(result->end, 0, 0, 0, 0);

    result->skip = SKIP_DAY;

    dump_daterange(result);

    return result;
}


static struct daterange *
force_morning (struct daterange *time)
{
    if (time->begin.hour >= 12) {
        time->begin.hour -= 12;
    }
    if (time->end.hour >= 12) {
        time->end.hour -= 12;
    }
    if (time->end.hour < time->begin.hour) {
        time->end.hour += 12;
    }

    return time;
}


static struct daterange *
force_evening (struct daterange *time)
{
    if (time->begin.hour < 17 && time->begin.hour > 4) {
        time->begin.hour += 12;
    }
    if (time->end.hour < 17 && time->end.hour > 4) {
        time->end.hour += 12;
    }
    return time;
}


static struct daterange *
new_datetime (struct strparser *p, struct daterange *date, struct daterange *time)
{
    struct daterange *result = new_daterange(p);;
    uint64_t begin_ts;
    uint64_t end_ts;

    memcpy(&result->begin, &date->begin, sizeof(BongoCalTime));
    memcpy(&result->end, &date->end, sizeof(BongoCalTime));

    result->begin.hour = time->begin.hour;
    result->begin.minute = time->begin.minute;
    result->begin.second = time->begin.second;
#if 0
    result->begin.tm_isdst = -1;
#endif

    result->end.hour = time->end.hour;
    result->end.minute = time->end.minute;
    result->end.second = time->end.second;
#if 0
    result->end.tm_isdst = -1;
#endif

    result->end = BongoCalTimeAdjust(result->end, 0, 0, 0, 0);

    begin_ts = BongoCalMkTime(&result->begin);
    end_ts = BongoCalMkTime(&result->end);
    if (end_ts < begin_ts) {
        skiptm (&result->end, SKIP_DAY, 1);
    } 
        
    result->skip = date->skip;

    dump_daterange(result);

    return result;
}


static struct daterange *
new_weekend (struct strparser *p, struct daterange *defaults)
{
    struct daterange *sunday;
    struct daterange *friday;
    struct daterange *result;
    uint64_t friday_ts;
    uint64_t sunday_ts;

    sunday = new_dayofweek(p, defaults, 0);
    friday = new_datetime(p, new_dayofweek(p, defaults, 5), 
                             new_time(p, defaults, 17, 0, 0));
    friday_ts = BongoCalMkTime(&friday->begin);
    sunday_ts = BongoCalMkTime(&sunday->end);

    if (friday_ts > sunday_ts) {
        skiptm(&friday->begin, SKIP_WEEK, -1);
        skiptm(&friday->end, SKIP_WEEK, -1);
    }
    result = new_daterange_from_dateranges(p, friday, sunday);
    result->skip = SKIP_WEEK;

    return result;
}


static struct daterange *
new_week (struct strparser *p, struct daterange *defaults)
{
    struct daterange *sunday;
    struct daterange *saturday;
    struct daterange *result;
    time_t saturday_ts;
    time_t sunday_ts;

    sunday = new_dayofweek(p, defaults, 0);
    saturday = new_dayofweek(p, defaults, 6);
    saturday_ts = BongoCalMkTime(&saturday->begin);
    sunday_ts = BongoCalMkTime(&sunday->end);

    if (saturday_ts < sunday_ts) {
        skiptm(&sunday->begin, SKIP_WEEK, -1);
        skiptm(&sunday->end, SKIP_WEEK, -1);
    }
    result = new_daterange_from_dateranges(p, sunday, saturday);
    result->skip = SKIP_WEEK;

    return result;
}



#define TWO_DIGIT_YEAR(_year) (1900 + ((_year) < 20 ? (_year + 100) : (_year)))


typedef union {
    int intval;
    struct daterange *dateval;
} ystype;
#define YYSTYPE ystype

#define YYDEBUG 1

static void
yyerror (struct strparser *p, const char *s)
{
    fprintf (stderr, "yyerror: %s\n", s);
}

int yylex (YYSTYPE *lvalp, struct strparser *p);


%}


%union {
    int intval;
    struct daterange *dateval;
}


%pure-parser        /* reentrant */
%lex-param { struct strparser *p }
%parse-param { struct strparser *p }


%token NEXT

%token TODAY
%token TOMORROW
%token YESTERDAY
%token WEEKEND
%token WEEK
%token MONTH
%token YEAR

%token OCLOCK


%token <intval> DTMONTH       /* "Jan", "March", etc. */
%token <intval> DTDAYOFWEEK   /* "Tues", "friday", etc. */

%token MIDNIGHT
%token NOON
%token AFTERNOON
%token EVENING
%token MORNING
%token <intval> AMPM

%token DAYSFROM
%token WEEKSFROM

%token A
%token AT
%token OF
%token ON
%token THE
%token THIS

%token <intval> NUM           /* \d+ */
%token <intval> YEARNUM       /* \d{4} */
%token <intval> ORDNUM        /* "9th", "22nd" etc. */

%token DASH                   /* '/', '.', or '-' */
%token COLON

%type <intval> ordnum
%type <intval> dtmonth
%type <dateval> daterange
%type <dateval> nameddaterange
%type <dateval> date
%type <dateval> datepartlist
%type <dateval> datepart
%type <dateval> formatteddate
%type <dateval> timerange
%type <dateval> timespan
%type <dateval> time

%right THRU         /* "through", "to", "until", etc */


%%


line           : /*empty*/
               | daterange       { p->date = $1;; }
               | error daterange { p->date = $2; }
               | daterange error { p->date = $1; }
;

timerange      : timespan
               | EVENING timespan { $$ = force_evening($2);                   }
               | MORNING timespan { $$ = force_morning($2);                   }
;

timespan       : time THRU time
    { 
        $$ = new_daterange_from_timeranges(p, $1, $3); 
    }
               | time
;

time           : NUM            { $$ = new_time(p, p->defaults, $1, 0, -1);   }
               | NUM COLON NUM  { $$ = new_time(p, p->defaults, $1, $3, -1);  }
               | NUM OCLOCK     { $$ = new_time(p, p->defaults, $1, 0, -1);   }
               | NUM AMPM       { $$ = new_time(p, p->defaults, $1, 0, $2);   }
               | NUM COLON NUM AMPM
    { 
        $$ = new_time(p, p->defaults, $1, $3, $4); 
    }
               | NOON           { $$ = new_time(p, p->defaults, 12, 0, 0);    }
               | MIDNIGHT       
    {
        $$ = new_daterange_from_timeranges (p, new_time(p, p->defaults, 23, 59, 0),
                                               new_time(p, p->defaults, 25, 0, 0));
    }
               | AFTERNOON
    { 
        $$ = new_daterange_from_timeranges (p, new_time(p, p->defaults, 12, 0, 0),
                                               new_time(p, p->defaults, 17, 59, 0));
    }
               | MORNING
    { 
        $$ = new_daterange_from_timeranges (p, new_time(p, p->defaults, 4, 0, 0),
                                               new_time(p, p->defaults, 11, 59, 0));
    }
               | EVENING     
    { 
        $$ = new_daterange_from_timeranges (p, new_time(p, p->defaults, 18, 0, 0),
                                               new_time(p, p->defaults, 23, 59, 0));
    }
               | AT time        { $$ = $2;                                    }
               | MORNING time   { $$ = $2;                                    }
               | AFTERNOON time { $$ = $2;                                    }
               | EVENING time   { $$ = $2;                                    }
               | THIS time      { $$ = $2;                                    }
;

daterange      : date
               | date THRU date
    {
        $$ = new_daterange_from_dateranges(p, $1, $3); 
    }
               | timespan DTDAYOFWEEK MORNING
    {
        $$ = new_datetime(p, new_dayofweek (p, p->defaults, $2), 
                             force_morning ($1));
    }
               | timespan DTDAYOFWEEK EVENING
    {
        $$ = new_datetime(p, new_dayofweek (p, p->defaults, $2), 
                             force_evening ($1));
    }
               | date timerange { $$ = new_datetime(p, $1, $2);               }
               | timerange
               | timerange date { $$ = new_datetime(p, $2, $1);               }
               | nameddaterange
;


nameddaterange : NEXT nameddaterange { $$ = daterange_skip($2, 1);            } 
               | WEEKEND        { $$ = new_weekend (p, p->defaults);          }
               | THIS WEEKEND   { $$ = new_weekend (p, p->defaults);          }
               | WEEK           { $$ = new_week    (p, p->defaults);          }
               | THIS WEEK      { $$ = new_week    (p, p->defaults);          }
               | MONTH          { $$ = new_month   (p, p->defaults, -1, -1);  }
               | THIS MONTH     { $$ = new_month   (p, p->defaults, -1, -1);  }
;

date           : NEXT date      { $$ = daterange_skip($2, 1);                 }
               | THIS date      { $$ = $2;                                    }
               | date timerange { $$ = new_datetime(p, $1, $2);               }
               | time date      { $$ = new_datetime(p, $2, $1);               }
               | datepartlist   
               | formatteddate
               | TOMORROW       
    { 
        $$ = daterange_skip (new_date(p, p->defaults, -1, -1, -1), 1); 
    }
               | YESTERDAY
    { 
        $$ = daterange_skip (new_date(p, p->defaults, -1, -1, -1), -1); 
    }
               | TODAY          { $$ = new_date(p, p->defaults, -1, -1, -1);  }
               | ON date        { $$ = $2;                                    }
               | NUM DAYSFROM date
    {
        $3->skip = SKIP_DAY;
        $$ = daterange_skip($3, $1);
    }
               | NUM WEEKSFROM date
    {
        $3->skip = SKIP_WEEK;
        $$ = daterange_skip ($3, $1);
    }
               | error date     { $$ = $2;                                    }
               | date error     { $$ = $1;                                    }
;

datepartlist   : datepart
               | datepartlist datepart
;

datepart       : DTDAYOFWEEK    { $$ = new_dayofweek(p, p->defaults, $1);     }
               | dtmonth        { $$ = new_month(p, p->defaults, $1, -1);     }
               | ordnum         { $$ = new_day_of_month(p, p->defaults, $1);  }
;

formatteddate  : NUM DASH NUM
    { 
        if ($1 > 12) { /* dd-mm */
            $$ = new_date(p, p->defaults, -1, $3-1, $1);
        } else if ($3 > 12) { /* mm-dd */
            $$ = new_date(p, p->defaults, -1, $1-1, $3);
        } else { /* assume mm-dd ... TODO use locale */
            $$ = new_date(p, p->defaults, -1, $1-1, $3);
        }
    }
               | NUM DASH YEARNUM
    {
        $$ = new_date (p, p->defaults, $3, $1-1, -1);         
    }

               | NUM DASH NUM DASH NUM 
    { 
        if ($1 > 12) { /* dd-mm-yy */
            $$ = new_date(p, p->defaults, TWO_DIGIT_YEAR($5), $3-1, $1);
        } else if ($3 > 12) { /* mm-dd-yy */
            $$ = new_date(p, p->defaults, TWO_DIGIT_YEAR($5), $1-1, $3);
        } else { /* assume mm-dd-yy ... TODO: use locale */
            $$ = new_date(p, p->defaults, TWO_DIGIT_YEAR($5), $1-1, $3);
        }
    }
               | YEARNUM DASH NUM DASH NUM
    {
            $$ = new_date(p, p->defaults, $1, $3-1, $5);
    }
               | NUM DASH NUM DASH YEARNUM 
    { 
        if ($1 > 12) { /* dd-mm-yyyy */
            $$ = new_date(p, p->defaults, $5, $3-1, $1);
        } else if ($3 > 12) { /* mm-dd-yyyy */
            $$ = new_date(p, p->defaults, $5, $1-1, $3);
        } else { /* assume mm-dd-yyyy TODO: use locale */
            $$ = new_date(p, p->defaults, $5, $1-1, $3);
        }
    }
               | NUM dtmonth    { $$ = new_date(p, p->defaults, -1, $2, $1);  }
               | DTMONTH NUM    { $$ = new_date(p, p->defaults, -1, $1, $2);  }
               | ordnum dtmonth { $$ = new_date(p, p->defaults, -1, $2, $1);  }
               | DTMONTH ordnum { $$ = new_date(p, p->defaults, -1, $1, $2);  }
               | DTMONTH YEARNUM 
    { 
        $$ = new_month(p, p->defaults, $1, $2);
    }
               | NUM dtmonth YEARNUM
    {
        $$ = new_date(p, p->defaults, $3, $2, $1);  
    }
               | DTMONTH NUM YEARNUM   
    {
        $$ = new_date(p, p->defaults, $3, $1, $2);  
    }
               | ordnum dtmonth YEARNUM 
    {
        $$ = new_date(p, p->defaults, $3, $2, $1);  
    }
               | DTMONTH ordnum YEARNUM 
    { 
        $$ = new_date(p, p->defaults, $3, $1, $2);  
    }
/*
               | DTDAYOFWEEK formatteddate
    {
        $$ = $2;
    }
*/
;

dtmonth        : DTMONTH
               | OF DTMONTH     { $$ = $2;                                  }

ordnum         : ORDNUM
               | THE ORDNUM     { $$ = $2;                                  }


%%

struct symboldef {
    int token;
    const char *symbol;
    int val;
};

static const struct symboldef tokens[] = {
    { THRU,         "through",      0  },
    { THRU,         "thru",         0  },
    { THRU,         "to",           0  },
    { THRU,         "until",        0  },
    { THRU,         "til",          0  },
    { THRU,         "till",         0  },


    { A,            "a",            0  }, 
    { AT,           "at",           0  }, 
    { ON,           "on",           0  },
    { THE,          "the",          0  },
    { OF,           "of",           0  },
    { THIS,         "this",         0  },

    { NEXT,         "next",         1  },

    { DAYSFROM,     "days",         0  }, /* hacky */
    { DAYSFROM,     "day",          0  },
    { WEEKSFROM,    "weeks",        0  },
    { WEEKSFROM,    "week",         0  },
    { WEEKSFROM,    "wk",           0  },
    { WEEKSFROM,    "wks",          0  },
    
    { TODAY,        "today",        0  },
    { TOMORROW,     "tomorrow",     0  },
    { YESTERDAY,    "yesterday",    0  },

    { NOON,         "noon",         0  },
    { MORNING,      "morning",      0  },
    { MORNING,      "morn",         0  },
    { AFTERNOON,    "afternoon",    0  },
    { EVENING,      "evening",      0  },
    { EVENING,      "eve",          0  },
    { EVENING,      "night",        0  },
    { EVENING,      "nite",         0  },
    { EVENING,      "tonite",       0  },
    { EVENING,      "tonight",      0  },
    { MIDNIGHT,     "midnight",     0  },
    { MIDNIGHT,     "midnite",      0  },

    { WEEKEND,      "weekend",      0  },
    { WEEKEND,      "wknd",         0  },

    { WEEK,         "week",         0  },
    { WEEK,         "wk",           0  },

    { MONTH,        "month",        0  },
    { MONTH,        "mnth",         0  },
    { MONTH,        "mth",          0  },
    { YEAR,         "year",         0  },
    { MONTH,        "yr",           0  },

    { DTDAYOFWEEK,  "sunday",       0  },
    { DTDAYOFWEEK,  "sun",          0  },
    { DTDAYOFWEEK,  "monday",       1  },
    { DTDAYOFWEEK,  "mon",          1  },
    { DTDAYOFWEEK,  "tuesday",      2  },
    { DTDAYOFWEEK,  "tues",         2  },
    { DTDAYOFWEEK,  "tue",          2  },
    { DTDAYOFWEEK,  "wednesday",    3  },
    { DTDAYOFWEEK,  "weds",         3  },
    { DTDAYOFWEEK,  "wed",          3  },
    { DTDAYOFWEEK,  "thursday",     4  },
    { DTDAYOFWEEK,  "thurs",        4  },
    { DTDAYOFWEEK,  "thur" ,        4  },
    { DTDAYOFWEEK,  "thu",          4  },
    { DTDAYOFWEEK,  "friday",       5  },
    { DTDAYOFWEEK,  "fri"   ,       5  },
    { DTDAYOFWEEK,  "saturday",     6  },
    { DTDAYOFWEEK,  "sat",          6  },

    { DTMONTH,      "january",      0  },
    { DTMONTH,      "jan",          0  },
    { DTMONTH,      "february",     1  },
    { DTMONTH,      "feb",          1  },
    { DTMONTH,      "march",        2  },
    { DTMONTH,      "mar",          2  },
    { DTMONTH,      "april",        3  },
    { DTMONTH,      "apr",          3  },
    { DTMONTH,      "may",          4  },
    { DTMONTH,      "june",         5  },
    { DTMONTH,      "jun",          5  },
    { DTMONTH,      "july",         6  },
    { DTMONTH,      "jul",          6  },
    { DTMONTH,      "august",       7  },
    { DTMONTH,      "aug",          7  },
    { DTMONTH,      "september",    8  },
    { DTMONTH,      "sept",         8  },
    { DTMONTH,      "sep",          8  },
    { DTMONTH,      "october",      9  },
    { DTMONTH,      "oct",          9  },
    { DTMONTH,      "november",     10 },
    { DTMONTH,      "nov",          10 },
    { DTMONTH,      "december",     11 },
    { DTMONTH,      "dec",          11 },

    { ORDNUM,       "first",        1  },
    { ORDNUM,       "second",       2  },
    { ORDNUM,       "third",        3  },
    { ORDNUM,       "fourth",       4  },
    { ORDNUM,       "fifth",        5  },
    { ORDNUM,       "sixth",        6  },
    { ORDNUM,       "seventh",      7  },
    { ORDNUM,       "eighth",       8  },
    { ORDNUM,       "ninth",        9  },
    { ORDNUM,       "tenth",        10 },
    { ORDNUM,       "eleventh",     11 },
    { ORDNUM,       "twelfth",      12 },
    { ORDNUM,       "thirteenth",   13 },
    { ORDNUM,       "fourteenth",   14 },
    { ORDNUM,       "fifteenth",    15 },
    { ORDNUM,       "sixteenth",    16 },
    { ORDNUM,       "seventeenth",  17 },
    { ORDNUM,       "eighteenth",   18 },
    { ORDNUM,       "nineteenth",   19 },
    { ORDNUM,       "twentieth",    20 },

    { AMPM,         "am",           0  },
    { AMPM,         "pm",           1  },
    { OCLOCK,       "o'clock",      0  },
    { OCLOCK,       "oclock",       0  },

    { 0,         NULL,              0  },
};


static const struct symboldef *
match_token (char *str)
{
    const struct symboldef *tok;

    for (tok = &tokens[0]; 
         tok->symbol; 
         tok++) 
    {
        if (!XplStrCaseCmp (tok->symbol, str)) {
            return tok;
        }
    }
    return NULL;
}


static const struct symboldef *
find_token (int token)
{
    const struct symboldef *tok;

    for (tok = &tokens[0]; 
         tok->symbol; 
         tok++)
    {
        if (tok->token == token) {
            return tok;
        }
    }

    return NULL;
}



int
yylex (YYSTYPE *lvalp, struct strparser *p)
{
    int c;
    int nextc;

    while (1) {
        /* skip whitespace */

        for (; c = p->str[p->idx], isspace(c); p->idx++) {
            if (p->dptr != p->desc && !isspace (p->dptr[-1])) {
                *(p->dptr++) = ' ';
            }
        }

        /* process number */
        if (isdigit(c)) {
            int numval = 0;

            for (; c = p->str[p->idx], isdigit(c); p->idx++) {
                numval *= 10;
                numval += c - '0';
            }
            lvalp->intval = numval;
            if (numval > 1000) {
                return YEARNUM;
            } else if (c) {
                nextc = p->str[p->idx+1];
                if ((('s' == c || 'S' == c) &&
                     ('t' == nextc || 'T' == nextc)) ||
                    (('n' == c || 'N' == c) &&
                     ('d' == nextc || 'D' == nextc)) || 
                    (('r' == c || 'R' == c) &&
                     ('d' == nextc || 'D' == nextc)) || 
                    (('t' == c || 'T' == c) &&
                     ('h' == nextc || 'H' == nextc)))
                {
                    /* ordinal number */
                    p->idx += 2;
                    if (',' == p->str[p->idx]) {
                        p->idx++;
                    }
                    return ORDNUM;
                }
            }
            return NUM;
        } else if (isalpha(c)) {
            char buf[256];
            int i = 0;
            const struct symboldef *token;

            for (; 
                 c = p->str[p->idx], c && (isalpha(c) || '\'' == c); 
                 p->idx++) 
            {
                buf[i++] = c;
                if (i >= 255) break;
            }
            buf[i] = 0;

            token = match_token (buf);
            if (token) {
                /* behold some awful hacking to get around the 1-token 
                   lookahead of bison by using the scanner: */

                if (A == token->token) {
                    /* valid A tokens: "a week from tuesday", etc.
                       ignore: everything
                    */
                    
                    char *skip;
                    
                    for (skip = &p->str[p->idx]; isspace(*skip); skip++);
                    if (!XplStrNCaseCmp ("week", skip, 4) ||
                        !XplStrNCaseCmp ("wk", skip, 2) ||
                        !XplStrNCaseCmp ("day", skip, 3)) 
                    {
                        lvalp->intval = 1;
                        return NUM;
                    } else {
                        token = NULL;
                    }
                } else if (AT == token->token) {
                    /* valid AT tokens: "lunch at noon", "lunch at 3pm"
                       ignore: "lunch at Legal Seafoods", etc.
                    */
                    
                    char *skip;
                    
                    for (skip = &p->str[p->idx]; isspace(*skip); skip++);
                    if (!isdigit(*skip) && XplStrNCaseCmp ("noon", skip, 4)) {
                        token = NULL;
                    }
                } else if (DAYSFROM == token->token ||
                           WEEKSFROM == token->token) 
                {
                    /* DAYSFROM token actually only sees DAYS; check for
                       and consume following "from"
                    */

                    int idx2;

                    for (idx2 = p->idx; isspace (p->str[idx2]); idx2++);

                    if (!XplStrNCaseCmp (&p->str[idx2], "from", 4)) {
                        p->idx = idx2 + 4;
                    } else if (!XplStrNCaseCmp (&p->str[idx2], "after", 5)) {
                        p->idx = idx2 + 5;
                    } else {
                        /* no "from" to consume, pass back WEEK token: */
                        if (WEEKSFROM == token->token) {
                            token = find_token (WEEK);
                        } else {
                            token = NULL;
                        }
                    }
                } else if (OF == token->token) {
                    /* valid OF tokens: "4th of july"
                       ignore: "fri meeting rest of team", etc.
                    */

                    char *skip;

                    for (skip = p->str + p->idx; isspace(*skip); skip++);
                    if (!isdigit(*skip) && *skip) {
                        char *ptr2;
                        char tmp;
 
                        for (ptr2 = skip; !isspace(*ptr2); ptr2++);
                        tmp = *ptr2;
                        *ptr2 = 0;
                        
                        if (!match_token (skip)) {
                            token = NULL;
                        }
                        *ptr2 = tmp;
                    }                   
                } else if (ON == token->token) {
                    /* valid ON tokens: "golf on friday", "interview on 3 July"
                       ignore: "3pm lecture on parsing technology"
                    */
                    char *skip;
                    
                    for (skip = p->str + p->idx; isspace(*skip); skip++);
                    if (!isdigit(*skip) && *skip) {
                        char *ptr2;
                        char tmp;
 
                        for (ptr2 = skip; !isspace(*ptr2); ptr2++);
                        tmp = *ptr2;
                        *ptr2 = 0;
                        
                        
                        if (!match_token (skip)) {
                            token = NULL;
                        }
                        *ptr2 = tmp;
                    }
                } else if (THE == token->token) {
                    /* ignore unless it's followed by an ordnum */

                    char *skip;
                    
                    for (skip = &p->str[p->idx]; isspace(*skip); skip++);
                    if (!isdigit(*skip)) {
                        char *ptr2;
                        char tmp;
                        const struct symboldef *nexttoken;
 
                        for (ptr2 = skip; !isspace(*ptr2); ptr2++);
                        tmp = *ptr2;
                        *ptr2 = 0;
                        nexttoken = match_token (skip);
                        *ptr2 = tmp;
                        if (!nexttoken) {
                            token = NULL;
                        } else if (!XplStrNCaseCmp ("week", skip, 4) ||
                                   !XplStrNCaseCmp ("wk", skip, 2) ||
                                   !XplStrNCaseCmp ("day", skip, 3))
                        {
                            lvalp->intval = 1;
                            return NUM;
                        } else if (ORDNUM != nexttoken->token) {
                            token = NULL;
                        }
                    } else {
                        while (isdigit(*++skip));
                        if (!skip) {
                            token = NULL;
                        } else {
                            nextc = skip[1];
                            if (!((('s' == *skip || 'S' == *skip) &&
                                   ('t' == nextc || 'T' == nextc)) ||
                                  (('n' == *skip || 'N' == *skip) &&
                                   ('d' == nextc || 'D' == nextc)) || 
                                  (('r' == *skip || 'R' == *skip) &&
                                   ('d' == nextc || 'D' == nextc)) || 
                                  (('t' == *skip || 'T' == *skip) &&
                                   ('h' == nextc || 'H' == nextc))))
                            {
                                token = NULL;
                            }
                        }
                    }
                } else if (THIS == token->token || 
                           THRU == token->token) 
                {
                    /* valid: "this weekend" "this monday"
                       ignore: "this stupid meeting at 3pm" 
                       valid: "1 to 3" "weds thru fri afternoon"
                       ignore: "try to wake up"
                    */
                    char *skip;

                    for (skip = p->str + p->idx; isspace(*skip); skip++);
                    if (!isdigit(*skip) && *skip) {
                        char *ptr2;
                        char tmp;
 
                        for (ptr2 = skip; !isspace(*ptr2); ptr2++);
                        tmp = *ptr2;
                        *ptr2 = 0;
                        
                        if (!match_token (skip)) {
                            token = NULL;
                        }
                        *ptr2 = tmp;
                    }
                }
            }
            if (token) {
                for (; c = p->str[p->idx], c && !isspace(c); p->idx++);
                lvalp->intval = token->val;
                return token->token;
            } else {
                memcpy (p->dptr, buf, i);
                p->dptr += i;
            }
        } else if ('-' == c || '/' == c || '.' == c) {
            nextc = p->str[p->idx+1];
            if (isdigit (nextc)) {
                p->idx++;
                return DASH;
            } else {
                *p->dptr++ = c;
                p->idx++;
            }
        } else if (':' == c) {
            nextc = p->str[p->idx+1];
            if (isdigit (nextc)) {
                p->idx++;
                return COLON;
            } else {
                *p->dptr++ = c;
                p->idx++;
            }
        } else if ('"' == c) {
            /* quoted strings are always descriptions */
            while ((c = p->str[++p->idx])) {
                if ('"' == c) {
                    ++p->idx;
                    break;
                } else if ('\\' == c) {
                    if (!p->str[++p->idx]) {
                        break;
                    }
                }
                *(p->dptr++) = c;
            }
        } else if (0 == c) {
            return c;
        } else {
            /* anything we don't recognize goes into the description */
            *(p->dptr++) = c;
            p->idx++;
        }
    }
}



/* CalCmdParseCommand
 *
 * Parses command from buf into calendar command struct cmd
 *
 * Return:
 *     non-zero on success
 */

int
CalCmdParseCommand (char *buf, CalCmdCommand *cmd, BongoCalTimezone *tz)
{
    struct strparser p;
    size_t size = sizeof(cmd->data);
    BongoCalTime now;
    int retval = 0;
    
    while (isspace(*buf)) { 
        buf++; 
    }
    if (!XplStrNCaseCmp(buf, "help", 4)) {
        cmd->type = CALCMD_HELP;
        strncpy(cmd->data, &buf[4], size);
        cmd->data[size-1] = '\0';        
        return 1;
    }
    if (!init_strparser(&p, buf)) {
        return retval;
    }    
    if (!setjmp(p.bailenv)) {
        if (BaseTime) {
            now = BongoCalTimeNewFromUint64(BaseTime, FALSE, NULL);
        } else {
            now = BongoCalTimeNow(NULL);
        }       
        cmd->begin = cmd->end = now;

        p.defaults = new_daterange_from_tms(&p, &cmd->begin, &cmd->end);

        yyparse(&p);

        while (isspace(*--p.dptr));
        *++p.dptr = 0;
        if (p.date) {
            memcpy(&cmd->begin, &p.date->begin, sizeof(BongoCalTime));
            memcpy(&cmd->end, &p.date->end, sizeof(BongoCalTime));

            /* FIXME: I think doing everything in utc then just
             * setting the timezone will work, but I'm not 100% sure */
            cmd->begin = BongoCalTimeSetTimezone(cmd->begin, tz);
            cmd->end = BongoCalTimeSetTimezone(cmd->end, tz);

            strncpy(cmd->data, p.desc, size);
            cmd->data[size-1] = 0;
            if (p.desc[0]) {
                cmd->type = CALCMD_NEW_APPT;
            } else {
                cmd->type = CALCMD_QUERY;
            }
            retval = 1;
        } 
    }

    destroy_strparser(&p);
    return retval;
}


int
CalCmdSetDebugMode (int flag)
{
    yydebug = flag;
    return flag;
}

int
CalCmdSetVerboseDebugMode (int flag)
{
    VerboseMode = flag;
    return flag;
}

time_t
CalCmdSetTestMode (time_t basetime)
{
    BaseTime = basetime;
    return BaseTime;
}
