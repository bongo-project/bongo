// This is simply a wrapper for ical
// Sadly, some installations use /usr/include/ical.h instead of the
// "correct" /usr/include/libical/ical.h

#ifdef HAVE_ICAL_H
#include <libical/ical.h>
#endif

#ifdef HAVE_OLD_ICAL_H
#include <ical.h>
#endif
