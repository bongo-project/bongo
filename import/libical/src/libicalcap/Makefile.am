#======================================================================
#  FILE: Makefile.am
#  CREATOR: acampi 
#  
#  $Id: Makefile.am,v 1.5 2003/10/21 18:28:26 ettore Exp $
#
#
# (C) COPYRIGHT 2003, Andrea Campi, mailto:a.campi@inet.it
#
# This program is free software; you can redistribute it and/or modify
# it under the terms of either: 
#
#    The LGPL as published by the Free Software Foundation, version
#    2.1, available at: http://www.fsf.org/copyleft/lesser.html
#
#  Or:
#
#    The Mozilla Public License Version 1.0. You may obtain a copy of
#    the License at http://www.mozilla.org/MPL/
#
#  
#
#======================================================================


lib_LTLIBRARIES = libicalcap-bongo.la

INCLUDES =			\
	-I$(top_builddir)	\
	-I$(top_srcdir)/src	\
	-I$(top_builddir)/src	\
	-I$(top_srcdir)/src/libical	\
	-I$(top_builddir)/libical	\
	-I$(srcdir)		\
	-DWITH_RR $(RRCAP_CFLAGS)

libicalcap_bongo_la_LDFLAGS = $(RRCAP_LIBS) -version-info 0:0:0


libicalcap_bongo_la_SOURCES =		\
	icalcap.c		\
	icalcap.h		\
	icalcap_impl.h		\
	icalcap_message.c	\
	icalcap_message_impl.h	\
	icalcap_rr.c		\
	icalcap_server.c	\
	icalcap_server.h	\
	icalcap_server_impl.h	\
	icalcap_session.c	\
	icalcap_session.h	\
	icalcap_session_impl.h	\
	icalcap_utils.c

libicalcap_bongoincludedir = $(includedir)

libicalcap_bongoinclude_HEADERS =	\
	icalcap.h		\
	icalcap_impl.h		\
	icalcap_message_impl.h	\
	icalcap_server.h	\
	icalcap_server_impl.h	\
	icalcap_session.h	\
	icalcap_session_impl.h


noinst_PROGRAMS = client

LDADD =  ../libicalss/libicalss-bongo.la ../libicalvcal/libicalvcal-bongo.la ../libical/libical-bongo.la libicalcap-bongo.la $(RRCAP_LIBS)


client_SOURCES = client.c

