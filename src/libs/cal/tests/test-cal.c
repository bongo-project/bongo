/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2001 Novell, Inc. All Rights Reserved.
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
#include <include/bongocheck.h>
#include <bongocal.h>

#ifdef BONGO_HAVE_CHECK

static BOOL ComponentMatches(icalcomponent *comp, icalcomponent *new, BOOL printErrors);

static char *
ReadStream(char *s, size_t size, void *d)
{
    char *c = fgets(s, size, (FILE*)d);
    
    return c;
}

static icalcomponent *
ParseIcalFile(char *filename)
{
    icalparser *parser;
    icalcomponent *comp;
    FILE *f;

    parser = icalparser_new();
    f = fopen(filename, "r");

    if (!f) {
        printf("couldn't open %s\n", filename);
        return NULL;
    }
    
    icalparser_set_gen_data(parser, f);

    comp = icalparser_parse(parser, ReadStream);
    icalcomponent_strip_errors(comp);

    return comp;
}

static BOOL
PropertyMatches(icalproperty *prop, icalproperty *new)
{
    icalparameter_xliccomparetype ret = icalvalue_compare(icalproperty_get_value(prop), icalproperty_get_value(new));

    if (ret == ICAL_XLICCOMPARETYPE_EQUAL) {
        return TRUE;
    } else {
        if (ret == 0) {
            printf("couldn't compare '%s' and '%s'\n", 
                   icalproperty_as_ical_string(prop),
                   icalproperty_as_ical_string(new));
            return TRUE;
#if 0            
            int cmp = strcmp(icalproperty_as_ical_string(prop), icalproperty_as_ical_string(new));
            return !cmp;
#endif
        }
        return FALSE;
    }
}

static BOOL
ComponentHasProperty(icalcomponent *new, icalproperty *prop)
{
    icalproperty *newprop;

    /* Checks that all the properties in orig are represented in
     * new.  This check is implemented quite slowly. */

    for (newprop = icalcomponent_get_first_property(new, icalproperty_isa(prop));
         newprop != NULL;
         newprop = icalcomponent_get_next_property(new, icalproperty_isa(prop))) {
        if (PropertyMatches(prop, newprop)) {
            return TRUE;
        }
    }

    return FALSE;
}

static BOOL
ComponentHasChild(icalcomponent *new, icalcomponent *comp)
{
    icalcomponent *newchild;
    
    for (newchild = icalcomponent_get_first_component(new, icalcomponent_isa(comp));
         newchild != NULL;
         newchild = icalcomponent_get_next_component(new, icalcomponent_isa(comp))) {
        if (ComponentMatches(comp, newchild, FALSE)) {
            return TRUE;
        }
    }
    
    return FALSE;
}

static BOOL 
ComponentMatches(icalcomponent *orig,
                 icalcomponent *new, 
                 BOOL printErrors)
{
    icalproperty *prop;
    icalcomponent *child;

    /* Checks that all the properties in orig are represented in
     * new.  This check is implemented quite slowly. */

    for (prop = icalcomponent_get_first_property(orig, ICAL_ANY_PROPERTY);
         prop != NULL;
         prop = icalcomponent_get_next_property(orig, ICAL_ANY_PROPERTY)) {
        if (!ComponentHasProperty(new, prop)) {
            if (printErrors) {
                printf("%s does not equal %s\n", icalcomponent_as_ical_string(orig), icalcomponent_as_ical_string(new));
                printf("doesn't have prop: %s\n", icalproperty_as_ical_string(prop));
            }

            return FALSE;
        }
    }


    for (child = icalcomponent_get_first_component(orig, ICAL_ANY_COMPONENT);
         child != NULL;
         child = icalcomponent_get_next_component(orig, ICAL_ANY_COMPONENT)) {
        if (!ComponentHasChild(new, child)) {
            if (printErrors) {
                printf("doesn't have child: %s\n", icalcomponent_as_ical_string(child));
            }
            
            return FALSE;
        }
    }
        
    return TRUE;
}

static BOOL
TestTranslation(icalcomponent *comp, BongoJsonObject *json)
{
    icalcomponent *newComp;
    BongoCalObject *cal;
    BongoJsonNode *json2;

    /* Check that converting to json and back leaves the object intact */
    json = BongoCalIcalToJson(comp);

    cal = BongoCalObjectNew(json);
    BongoCalObjectStripSystemTimezones(cal);

    printf("converted to %s\n", BongoJsonObjectToString(json));

    fail_unless(BongoJsonParseString(BongoJsonObjectToString(json), &json2) == BONGO_JSON_OK);

    newComp = BongoCalJsonToIcal(json);

    /*printf("converted back to %s\n", icalcomponent_as_ical_string(newComp));*/

    fail_unless(ComponentMatches(comp, newComp, TRUE));

    return TRUE;
}

static BOOL
TestRecur(BongoJsonObject *test, BongoJsonObject *calJson)
{
    BongoCalObject *cal;
    BongoJsonObject *child;
    BongoCalTime recurStart;
    BongoCalTime recurEnd;    
    BongoArray *expect;
    BongoArray *occs;
    BongoCalOccurrence occ;
    const char *str;
    unsigned int i;
    
    cal = BongoCalObjectNew(calJson);
    
    if (BongoJsonObjectResolveString(test, "recur/start", &str) != BONGO_JSON_OK) {
        printf("didn't get start\n");
        return FALSE;
    }

    recurStart = BongoCalTimeParseIcal(str);

    if (BongoJsonObjectResolveString(test, "recur/end", &str) != BONGO_JSON_OK) {
        printf("didn't get end\n");
        return FALSE;
    }

    if (BongoJsonObjectResolveObject(test, "recur/objectStart", &child) == BONGO_JSON_OK) {
        const char *time = NULL;
        const char *tzid = NULL;
        BongoCalTime start;
        BongoCalTime end;
        char buffer[1024];
        
        BongoJsonObjectResolveString(test, "recur/objectStart/value", &time);
        BongoJsonObjectResolveString(test, "recur/objectStart/params/tzid", &tzid);
        start = BongoCalTimeParseIcal(time);
        start = BongoCalTimeSetTimezone(start, BongoCalObjectGetTimezone(cal, tzid));

        if (BongoCalTimeCompare(start, BongoCalObjectGetStart(cal)) != 0) {
            BongoCalTimeToIcal(BongoCalObjectGetStart(cal), buffer, sizeof(buffer));
            printf("object start time failed, expected %s got %s\n", time, buffer);
        }
        fail_unless(BongoCalTimeCompare(start, BongoCalObjectGetStart(cal)) == 0);


        BongoJsonObjectResolveString(test, "recur/objectEnd/value", &time);
        BongoJsonObjectResolveString(test, "recur/objectEnd/params/tzid", &tzid);
        end = BongoCalTimeParseIcal(time);
        end = BongoCalTimeSetTimezone(end, BongoCalObjectGetTimezone(cal, tzid));

        if (BongoCalTimeCompare(end, BongoCalObjectGetEnd(cal)) >= 0) {
            BongoCalTimeToIcal(BongoCalObjectGetEnd(cal), buffer, sizeof(buffer));
            printf("object end time failed, expected %s got %s\n", time, buffer);
        }
        fail_unless(BongoCalTimeCompare(end, BongoCalObjectGetEnd(cal)) < 0);
    }


    recurEnd = BongoCalTimeParseIcal(str);

    occs = BongoArrayNew(sizeof(BongoCalOccurrence), 16);
    BongoCalObjectCollect(cal, recurStart, recurEnd, NULL, TRUE, occs);

    BongoCalOccurrencesSort(occs);

    printf("cal json is now %s\n", BongoJsonObjectToString(calJson));
    printf("occurrences are %s\n", BongoJsonArrayToString(BongoCalOccurrencesToJson(occs, BongoCalTimezoneGetSystem("/bongo/America/New_York"))));

    if (BongoJsonObjectResolveArray(test, "recur/expect", &expect) != BONGO_JSON_OK) {
        return TRUE;
    }

    printf("got %d occurrences, expected %d\n", occs->len, expect->len);
    
    fail_unless(occs->len == expect->len);
    if (occs->len != expect->len) {
        return FALSE;
    }

    for (i = 0; i < occs->len; i++) {
        BongoJsonObject *expectedInstance;
        
        occ = BongoArrayIndex(occs, BongoCalOccurrence, i);
        if (BongoJsonArrayGetObject(expect, i, &expectedInstance) == BONGO_JSON_OK) {
            const char *time = NULL;
            const char *tzid = NULL;
            BongoCalTime t;
            BongoCalTime t2;
            char buffer[1024];

            t = occ.start;

            BongoJsonObjectResolveString(expectedInstance, "dtstart/value", &time);
            BongoJsonObjectResolveString(expectedInstance, "dtstart/params/tzid", &tzid);
            t2 = BongoCalTimeParseIcal(time);
            t2.tz = BongoCalObjectGetTimezone(cal, tzid);
            t2.tzid = tzid;

            if (BongoCalTimeCompare(t, t2) != 0) {
                BongoCalTimeToIcal(t, buffer, sizeof(buffer));
                printf("comparing %s (%s) to %s (%s) failed\n", buffer, t.tzid, time, t2.tzid);
            }
            
            fail_unless(BongoCalTimeCompare(t, t2) == 0);


            BongoJsonObjectResolveString(expectedInstance, "dtend/value", &time);
            BongoJsonObjectResolveString(expectedInstance, "dtend/params/tzid", &tzid);

            t = occ.end;
            t2 = BongoCalTimeParseIcal(time);
            t2.tz = BongoCalObjectGetTimezone(cal, tzid);

            fail_unless(BongoCalTimeCompare(t, t2) == 0);
        }
    }

    return TRUE;
}

static BOOL
TestCalObj(const char *name)
{
    BongoJsonNode *testNode;
    BongoJsonObject *test;
    const char *str;
    FILE *f;
    char filename[PATH_MAX];
    icalcomponent *originalComponent;
    BongoJsonObject *json;
    BongoCalObject *cal;
    int numSplit;
    BOOL doTest;


    snprintf(filename, sizeof(filename), "data/%s.test", name);
    printf("opening %s\n", filename);
    f = fopen(filename, "r");

    if (!f) {
        printf("couldn't open %s\n", filename);
        return FALSE;
    }
    
    if (BongoJsonParseFile(f, 0, &testNode) != BONGO_JSON_OK || testNode->type != BONGO_JSON_OBJECT) {
        fclose(f);
        return FALSE;
    }

    fclose(f);

    test = BongoJsonNodeAsObject(testNode);    

    snprintf(filename, sizeof(filename), "data/%s.ics", name);
    originalComponent = ParseIcalFile(filename);

    json = BongoCalIcalToJson(originalComponent);

#if 1
    printf("original is %s\n", BongoJsonObjectToString(json));
#endif

    if (BongoJsonObjectResolveInt(test, "split", &numSplit) == BONGO_JSON_OK) {
        BongoArray *split;
        BongoJsonNode *split1;
        
        split = BongoCalSplit(json);
#if 0
        split1 = BongoArrayIndex(split, BongoJsonNode *, 0);
        printf("first split is %s\n", BongoJsonNodeToString(split1));
#endif

        printf("comparing %d to %d\n", split->len, numSplit);
        fail_unless(split->len == numSplit);
        return TRUE;
    }

    cal = BongoCalObjectNew(json);

    if (BongoJsonObjectResolveBool(test, "translate", &doTest) == BONGO_JSON_OK && doTest) {
        printf("testing ics<->json translation\n");
        TestTranslation(originalComponent, json);
    }

    if (BongoJsonObjectResolveString(test, "summary", &str) == BONGO_JSON_OK && str) {
        printf("comparing %s to %s\n", BongoCalObjectGetSummary(cal), str);
        fail_unless(!strcmp(BongoCalObjectGetSummary(cal), str));
    }

    if (BongoJsonObjectResolveString(test, "recur/start", &str) == BONGO_JSON_OK && str) {
        printf("testing recurrence\n");
        TestRecur(test, json);
    }

    return TRUE;
}

static BOOL
TestResolve(const char *name)
{
    char filename[PATH_MAX];
    BongoJsonNode *node;
    BongoCalObject *cal;
    FILE *f;
    icalcomponent *comp;
    
    snprintf(filename, sizeof(filename), "data/%s.json", name);
    printf("opening %s\n", filename);
    f = fopen(filename, "r");

    if (!f) {
        printf("couldn't open %s\n", filename);
        return FALSE;
    }
    
    if (BongoJsonParseFile(f, 0, &node) != BONGO_JSON_OK || node->type != BONGO_JSON_OBJECT) {
        fclose(f);
        return FALSE;
    }

    fclose(f);
    
    cal = BongoCalObjectNew(BongoJsonNodeAsObject(node));
    BongoCalObjectResolveSystemTimezones(cal);

    //printf("converted to %s\n", BongoJsonObjectToString(BongoCalObjectGetJson(cal)));

    comp = BongoCalJsonToIcal(BongoCalObjectGetJson(cal));

    //printf("ical is %s\n", icalcomponent_as_ical_string(comp));

    return TRUE;
}


START_TEST(cal) 
{
    printf("\n");

    MemoryManagerOpen("testcal");

    BongoCalInit(".");

    TestCalObj("unicode");
    TestCalObj("recur1");
    TestCalObj("recur2");
    TestCalObj("recur3");
    TestCalObj("recur4");
    TestCalObj("recur5");
    TestCalObj("recur6");
    TestCalObj("recur7");
    TestCalObj("recur8");
    TestCalObj("recur9");
    TestCalObj("recur10");
    TestCalObj("recur11");
    TestCalObj("recur12");
    TestCalObj("bruins");
    TestCalObj("cleveland");
    TestCalObj("us-holidays");
    TestCalObj("lincoln");
    TestCalObj("presidents-day");
    TestCalObj("labor-day");
    TestCalObj("angle");
    TestCalObj("comma");
    TestCalObj("split1");
    TestCalObj("dup-timezones");
    TestResolve("timezones");
    // TODO
    // fail_unless(1==1);
    // fail_if(1 == 2);
}
END_TEST

static void
TestUtcTime(uint64_t u)
{
    BongoCalTimeNewFromUint64(u, FALSE, NULL);    
}

static void
TestTime(int year, int month, int day, int hour, int minute, int second)
{
    struct tm tm = {0, };
    
    BongoCalTime t = BongoCalTimeEmpty();
    t.isUtc = TRUE;
    BongoCalTime t2;
    uint64_t u;
    time_t timet;
    
    t.year = year;
    t.month = month;
    t.day = day;
    t.hour = hour;
    t.minute = minute;
    t.second = second;

    u = BongoCalTimeAsUint64(t);
    if (year < 2023) {
        tm.tm_year = year - 1900;
        tm.tm_mon = month;
        tm.tm_mday = day;
        tm.tm_hour = hour;
        tm.tm_min = minute;
        tm.tm_sec = second;
        tm.tm_isdst = 0;
        
        timet = timegm(&tm);
    
        fail_unless(timet == (time_t)u);
    }

    t2 = BongoCalTimeNewFromUint64(u, FALSE, NULL);
    
    fail_unless(BongoCalTimeCompare(t, t2) == 0);
}

START_TEST(timetest) 
{
    printf("\n");

    MemoryManagerOpen("testcal");
    BongoCalInit(".");

    TestUtcTime(18446744073709551615ULL);

    TestTime(1970, 0, 1, 0, 0, 0);
    TestTime(2000, 3, 2, 0, 0, 0);
    TestTime(1999, 2, 1, 0, 0, 0);
    TestTime(1970, 1, 23, 0, 0, 0);
    TestTime(2099, 1, 23, 0, 0, 0);
    
    // TODO
    // fail_unless(1==1);
    // fail_if(1 == 2);
}
END_TEST

static void
TestTz(const char *tz1, const char *time1, const char *tz2, const char *time2)
{
    BongoCalTime t1, t2;
    char buffer[1024];
    
    t1 = BongoCalTimeParseIcal(time1);

    if (tz1) {
        assert(BongoCalTimezoneGetSystem(tz1) != NULL);
        t1 = BongoCalTimeSetTimezone(t1, BongoCalTimezoneGetSystem(tz1));
    }

    assert(BongoCalTimezoneGetSystem(tz2) != NULL);
    t2 = BongoCalTimezoneConvertTime(t1, BongoCalTimezoneGetSystem(tz2));
    BongoCalTimeToIcal(t2, buffer, 1024);
    
    fail_unless(!strcmp(time2, buffer));
    fail_unless(BongoCalTimeCompare(t1, t2) == 0);
}

START_TEST(tztest) 
{
    printf("\n");

    MemoryManagerOpen("testcal");
    BongoCalInit(".");

    TestTz("/bongo/America/Los_Angeles",
           "20060331T045300",
           "/bongo/America/New_York",
           "20060331T075300");

    TestTz(NULL,
           "20060331T045300",
           "/bongo/America/New_York",
           "20060331T045300");
    
    // TODO
    // fail_unless(1==1);
    // fail_if(1 == 2);
}
END_TEST

// TODO Write your tests above, and/or
// pound-include other tests of your own here.
START_CHECK_SUITE_SETUP("libbongocal tests")
    CREATE_CHECK_CASE   (tc_core  , "Core"   );
    CHECK_SUITE_ADD_CASE(top_suite, tc_core  );
    CHECK_CASE_ADD_TEST (tc_core  , timetest);
    CHECK_CASE_ADD_TEST (tc_core  , cal);
    CHECK_CASE_ADD_TEST (tc_core  , tztest);
    // TODO register additional tests here
END_CHECK_SUITE_SETUP
#else
SKIP_CHECK_TESTS
#endif
