// Test the Xpl DNS resolution code

#include <xpl.h>
#include <bongocheck.h>

#ifdef BONGO_HAVE_CHECK

START_TEST(test1) 
{
    fail_unless(1==1);
}
END_TEST

START_CHECK_SUITE_SETUP("Testing the Xpl DNS routines")
    CREATE_CHECK_CASE   (tc_core  , "Core"   );
    CHECK_SUITE_ADD_CASE(top_suite, tc_core  );
    CHECK_CASE_ADD_TEST (tc_core  , test1    );
END_CHECK_SUITE_SETUP

#else

SKIP_CHECK_TESTS

#endif
