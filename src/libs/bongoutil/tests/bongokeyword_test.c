#include <config.h>
#include <xpl.h>
#include <memmgr.h>
#include <bongoutil.h>

#define TIME_SEARCH

typedef struct {
    char *name;
    unsigned long weight;
} WeightEntry;

WeightEntry WeightTable[] =  {
    {"ham", 16},
    {"turkey", 32},
    {"bacon", 12},
    {"chicken", 16},
    {"beef", 48},
    {"mastard", 8},
    {"ketchup", 16},
    {"Mayonnaise", 10},
    {"gravy", 10},
    {"crab", 10},
    {"salmon", 10},
    {"shrimp", 10},
    {"sardines", 10},
    {"tuna", 10},
    {"apricot", 10},
    {"cherries", 10},
    {"cocktail", 10},
    {"cranberry", 10},
    {"grapefruit", 10},
    {"bananas", 10},
    {"strawberries", 10},
    {"strawberry", 10},
    {"peach", 10},
    {"pear", 10},
    {"orange", 10},
    {"pineapple", 10},
    {"cheese", 10},
    {"corn", 10},
    {"onion", 10},
    {"mushroom", 10},
    {"juice", 10},
    {"soda", 10},
    {"cereal", 2},
    {"milk", 72},
    {"eggs", 3},
    {"applesauce", 20},
    {"apple", 5},
    {"soup", 8},
    {"noodles", 56},
    {"sugar", 48},
    {"Kellogs Corn Flakes", 20},
    {"Quaker Oatmeal", 24},
    {NULL, 0},
};

BongoKeywordIndex *WeightIndex;

#if defined(TIME_SEARCH)
static long
TraditionalFindString(WeightEntry *WeightTable, char *searchString)
{
    unsigned long i;

    i = 0;

    while (WeightTable[i].name != NULL) {
	if (XplStrCaseCmp(WeightTable[i].name, searchString) != 0) {
	    i++;
	    continue;
	}
	
	return(i);
    }
    return(-1);
}

static long
TraditionalFindStringBegins(WeightEntry *WeightTable, char *searchString)
{
    unsigned long i;

    i = 0;

    while (WeightTable[i].name != NULL) {
	if (XplStrNCaseCmp(WeightTable[i].name, searchString, strlen(WeightTable[i].name)) != 0) {
	    i++;
	    continue;
	}
	
	return(i);
    }
    return(-1);
}

static uint32_t
StringHash(const void *key)
{
    char *s = (char *) key;
    uint32_t b = 1;
    uint32_t h = 0;

    for (; *s && ' ' != *s; s++) {
        h += b * tolower(*s);
        b = (b << 5) - b;
    }
    return h;
}

static int
StringCmp(const void *cmda, const void *cmdb)
{
    char *a = (char *) cmda;
    char *b = (char *) cmdb;

#if 0
    XplStrCaseCmp(a, b);
#else
    while(*a) {
        if (tolower(*a) != tolower(*b)) {
            if (!*b && !*a) {
                return 0;
            } else {
                return -1;
            }
        }
        a++;
        b++;
    }
    return 0;
#endif
}
#endif

START_TEST(keywordfind)
{
    long tableId;
    long i;
#if defined(TIME_SEARCH)
    long j;
    unsigned long startTime;
    unsigned long endTime;

    BongoHashtable *table;

#endif

    MemoryManagerOpen("BongoKeyword_Test");

    BongoKeywordIndexCreateFromTable(WeightIndex, WeightTable, .name, TRUE);

#if defined(TIME_SEARCH)


    XplGetHighResolutionTime(startTime);
    for (j = 0; j < 10000; j++) {
	i = 0;

	while (WeightTable[i].name != NULL) {
	    tableId = BongoKeywordFind(WeightIndex, WeightTable[i].name);
	    i++;
	}

	tableId = BongoKeywordFind(WeightIndex, "rocks");
    }
    XplGetHighResolutionTime(endTime);
    XplConsolePrintf("\n\nExact Match Results:\n");
    XplConsolePrintf("BongoKeywordFind:         %12d\n", (int)(endTime - startTime));

    table = BongoHashtableCreate(64, StringHash, StringCmp);
    if (table) {
	i = 0;

	while (WeightTable[i].name != NULL) {
            BongoHashtablePut(table, WeightTable[i].name, (void *)i);
	    i++;
	}
    }

    XplGetHighResolutionTime(startTime);
    for (j = 0; j < 10000; j++) {
	i = 0;

	while (WeightTable[i].name != NULL) {
	    tableId = (long)BongoHashtableGet(table, WeightTable[i].name);
	    i++;
	}

	tableId = (long)BongoHashtableGet(table, "rocks");
    }
    XplGetHighResolutionTime(endTime);

    XplConsolePrintf("BongoHashtableGet         %12d\n", (int)(endTime - startTime));
    XplGetHighResolutionTime(startTime);
    for (j = 0; j < 10000; j++) {

	i = 0;

	while (WeightTable[i].name != NULL) {
	    tableId = TraditionalFindString(WeightTable, WeightTable[i].name);
	    i++;
	}

	tableId = TraditionalFindString(WeightTable, "rocks");
    }
    XplGetHighResolutionTime(endTime);
    XplConsolePrintf("Traditional Search Time: %12d\n", (int)(endTime - startTime));

    XplGetHighResolutionTime(startTime);
    for (j = 0; j < 10000; j++) {
	i = 0;

	while (WeightTable[i].name != NULL) {
	    tableId = BongoKeywordBegins(WeightIndex, WeightTable[i].name);
	    i++;
	}

	tableId = BongoKeywordFind(WeightIndex, "rocks");
    }
    XplGetHighResolutionTime(endTime);
    XplConsolePrintf("\nBegins With Results:\n");
    XplConsolePrintf("KeywordSearch Time:      %12d\n", (int)(endTime - startTime));

    XplGetHighResolutionTime(startTime);
    for (j = 0; j < 10000; j++) {

	i = 0;

	while (WeightTable[i].name != NULL) {
	    tableId = TraditionalFindStringBegins(WeightTable, WeightTable[i].name);
	    i++;
	}

	tableId = TraditionalFindString(WeightTable, "rocks");
    }
    XplGetHighResolutionTime(endTime);
    XplConsolePrintf("Traditional Search Time: %12d\n", (int)(endTime - startTime));

#endif

    /* test case-insensitive exact match */
    i = 0;

    while (WeightTable[i].name != NULL) {
	tableId = BongoKeywordFind(WeightIndex, WeightTable[i].name);
	fail_unless(tableId == i);
	i++;
    }

    tableId = BongoKeywordFind(WeightIndex, "apple");
    fail_unless(WeightTable[tableId].weight  == (unsigned long)5);

    tableId = BongoKeywordBegins(WeightIndex, "apples");
    fail_unless(tableId  == -1);

    tableId = BongoKeywordFind(WeightIndex, "ApPlE");
    fail_unless(WeightTable[tableId].weight  == (unsigned long)5);

    tableId = BongoKeywordFind(WeightIndex, "AppleSauce");
    fail_unless(WeightTable[tableId].weight  == (unsigned long)20);

    tableId = BongoKeywordFind(WeightIndex, "rocks");
    fail_unless(tableId  == -1);

    tableId = BongoKeywordFind(WeightIndex, "");
    fail_unless(tableId  == -1);

    /* test case-insensitive begins with */
    i = 0;

    while (WeightTable[i].name != NULL) {
	tableId = BongoKeywordBegins(WeightIndex, WeightTable[i].name);
	fail_unless(tableId == i);
	i++;
    }

    tableId = BongoKeywordBegins(WeightIndex, "apple ");
    fail_unless(WeightTable[tableId].weight  == (unsigned long)5);

    tableId = BongoKeywordBegins(WeightIndex, "ApPlE");
    fail_unless(WeightTable[tableId].weight  == (unsigned long)5);

    tableId = BongoKeywordBegins(WeightIndex, "AppleSauces");
    fail_unless(WeightTable[tableId].weight  == (unsigned long)20);

    tableId = BongoKeywordBegins(WeightIndex, "rocks");
    fail_unless(tableId  == -1);

    tableId = BongoKeywordBegins(WeightIndex, "");
    fail_unless(tableId  == -1);


    BongoKeywordIndexFree(WeightIndex);

    /* test case-sensitive exact match */
    BongoKeywordIndexCreateFromTable(WeightIndex, WeightTable, .name, FALSE);

    i = 0;

    while (WeightTable[i].name != NULL) {
	tableId = BongoKeywordFind(WeightIndex, WeightTable[i].name);
	fail_unless(tableId == i);
	i++;
    }

    tableId = BongoKeywordFind(WeightIndex, "apple");
    fail_unless(WeightTable[tableId].weight  == (unsigned long)5);

    tableId = BongoKeywordFind(WeightIndex, "ApPlE");
    fail_unless(tableId  == -1);

    tableId = BongoKeywordFind(WeightIndex, "AppleSauce");
    fail_unless(tableId  == -1);

    tableId = BongoKeywordFind(WeightIndex, "rocks");
    fail_unless(tableId  == -1);

    tableId = BongoKeywordFind(WeightIndex, "");
    fail_unless(tableId  == -1);

    /* test case-sensitive begins with */
    i = 0;

    while (WeightTable[i].name != NULL) {
	tableId = BongoKeywordBegins(WeightIndex, WeightTable[i].name);
	fail_unless(tableId == i);
	i++;
    }

    tableId = BongoKeywordBegins(WeightIndex, "apple ");
    fail_unless(WeightTable[tableId].weight  == (unsigned long)5);

    tableId = BongoKeywordBegins(WeightIndex, "ApPlE");
    fail_unless(tableId  == -1);

    tableId = BongoKeywordBegins(WeightIndex, "applesauces");
    fail_unless(WeightTable[tableId].weight  == (unsigned long)20);

    tableId = BongoKeywordBegins(WeightIndex, "rocks");
    fail_unless(tableId  == -1);

    tableId = BongoKeywordBegins(WeightIndex, "");
    fail_unless(tableId  == -1);


    BongoKeywordIndexFree(WeightIndex);

    MemoryManagerClose("BongoKeyword_Test");
}
END_TEST
