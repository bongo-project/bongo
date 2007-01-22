#include <config.h>
#include <xpl.h>
#include <memmgr.h>
#include <bongoutil.h>

START_TEST(stringops)
{
    MemoryManagerOpen("StringOps_Test");
    char string[1024];

    printf ("Testing trims...");

    fail_unless(!strcmp(BongoStrTrim(MemStrdup("  BongoTrim\r\n")), "BongoTrim"));
    fail_unless(!strcmp(BongoStrTrim(MemStrdup("BongoTrim\r\n")), "BongoTrim"));
    fail_unless(!strcmp(BongoStrTrim(MemStrdup("  BongoTrim")), "BongoTrim"));
    fail_unless(!strcmp(BongoStrTrim(MemStrdup("BongoTrim")), "BongoTrim"));
    fail_unless(!strcmp(BongoStrTrim(MemStrdup("  ")), ""));
    fail_unless(!strcmp(BongoStrTrim(MemStrdup("")), ""));


    fail_unless(!strcmp(BongoStrTrimDup("  BongoTrim\r\n"), "BongoTrim"));
    fail_unless(!strcmp(BongoStrTrimDup("BongoTrim\r\n"), "BongoTrim"));
    fail_unless(!strcmp(BongoStrTrimDup("  BongoTrim"), "BongoTrim"));
    fail_unless(!strcmp(BongoStrTrimDup("BongoTrim"), "BongoTrim"));
    fail_unless(!strcmp(BongoStrTrimDup("  "), ""));
    fail_unless(!strcmp(BongoStrTrimDup(""), ""));

    MemoryManagerClose("StringOps_Test");
}
END_TEST
