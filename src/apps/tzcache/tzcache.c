#include <config.h>
#include <bongocal.h>
#include <msgapi.h>

int 
main(int argc, char **argv)
{
    char path[PATH_MAX];

    if (XplSetRealUser(MsgGetUnprivilegedUser()) < 0) {    
        printf("bongotzcache: Could not drop to unpriviliged user '%s'\n", MsgGetUnprivilegedUser());
        return 1;
    }

    MemoryManagerOpen("bongotzcache");

    MsgInit();

    snprintf(path, PATH_MAX, "%s/%s", MsgGetDBFDir(NULL), "bongo-tz.cache");
    unlink(path);
    
    BongoCalInit(MsgGetDBFDir(NULL));

    return 0;
}
