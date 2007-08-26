// Test the Xpl DNS resolution code

#include <xpl.h>
#include <xpldns.h>
#include <memmgr.h>
#include <bongocheck.h>

#ifdef BONGO_HAVE_CHECK

#define MEM_NAME	"check-xpl-dns"

START_TEST(test1) 
{
	XplDns_MxLookup *mx;
	XplDns_IpList *list;

	MemoryManagerOpen(MEM_NAME);

   	fail_unless(XplDnsInit() == 0);

	mx = XplDnsNewMxLookup("bongo-project.org");

	fail_unless(mx != NULL);
	fail_unless(mx->status == XPLDNS_SUCCESS);

	XplDnsFreeMxLookup(mx);

	MemoryManagerClose(MEM_NAME);
}
END_TEST

START_CHECK_SUITE_SETUP("Xpl DNS routine tests")
    CREATE_CHECK_CASE   (tc_core  , "Core"   );
    CHECK_SUITE_ADD_CASE(top_suite, tc_core  );
    CHECK_CASE_ADD_TEST (tc_core  , test1    );
END_CHECK_SUITE_SETUP

#else

SKIP_CHECK_TESTS

#endif
