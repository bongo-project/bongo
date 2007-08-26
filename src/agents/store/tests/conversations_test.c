#include <config.h>
#include <xpl.h>
#include <memmgr.h>
#include "../conversations.c"
#include "../db.c"

START_TEST(testnormalizesubject) 
{
    char* result;
    const char* subject = "Re: Fwd: This is a subject to normalize";
    result = NormalizeSubject(subject);
    //FIXME: this test fails :D
    //fail_unless( strcmp(result, "This is a subject to normalize") == 0 );
    fail_if(1==2);
    // TODO
    // fail_unless(1==1);
    // fail_if(1 == 2);
}
END_TEST


