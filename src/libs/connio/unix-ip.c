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

#include <config.h>

#if defined(SOLARIS) || defined(LINUX) || defined(S390RH)
#ifndef BSD_COMP
/* Needed for SIOCGIF* ioctls on Solaris. */
#define BSD_COMP 1
#endif
#include <errno.h>

#include <xpl.h>
#include <xplutil.h>

#include <connio.h>

#include "conniop.h"

#include <sys/utsname.h>
#include <netdb.h>
#include <arpa/inet.h>
#include <sys/poll.h>

#define IPerror()           errno
#define IPclear()           errno = 0

#define CONNECTION_TIMEOUT  (30*60)

/*  fixme - Don't abuse a consumer's global variables!!!
*/
extern int Exiting;

unsigned long
XplGetHostIPAddress(void)
{
	unsigned long	Address;
	struct utsname	name;
	struct hostent	*he;

	uname(&name);

	he = gethostbyname(name.nodename);
	if (he) {
		memcpy(&Address, he->h_addr_list[0], sizeof(Address));
		return(Address);
	}

	return(inet_addr("127.0.0.1"));
}


#ifndef __unp_ifi_h
#define __unp_ifi_h

/* Get interface list - adapted from Richard Steven's book */
#include <sys/ioctl.h>
#include <net/if.h>

struct ifi_info {
  short ifi_flags; /* IFF_xxx constants from <net/if.h> */
  short ifi_myflags; /* our own IFI_xxx flags */
  struct sockaddr *ifi_addr; /* primary address */
  struct sockaddr *ifi_brdaddr; /* broadcast address */
  struct sockaddr *ifi_dstaddr; /* destination address */
  struct ifi_info *ifi_next; /* next of these structures */
};

#define IFI_ALIAS 1/* ifi_addr is an alias */

#endif /* __unp_ifi_h */

static struct ifi_info *
get_ifi_info(int family, int doaliases)
{
  struct ifi_info *ifi, *ifihead, **ifipnext;
  int sockfd, len, lastlen, flags, myflags;
  char *ptr,  *buf = NULL, lastname[IFNAMSIZ], *cptr;
  struct ifconf ifc;
  struct ifreq *ifr, ifrcopy;
  struct sockaddr_in *sinptr;
  
  sockfd = socket(AF_INET, SOCK_DGRAM, 0);
  
  lastlen = 0;
#ifdef SIOCGIFNUM
#ifndef MAXIFS
#define MAXIFS 256
#endif
  if (ioctl(sockfd, SIOCGIFNUM, (char *)&len) < 0) {
    len = MAXIFS;
  }
  len *= sizeof(struct ifreq);
  buf = malloc(len);
  ifc.ifc_len = len;
  ifc.ifc_buf = buf;
  if (ioctl(sockfd, SIOCGIFCONF, &ifc) < 0) {
    perror("getifi_info ioctl error");
  }
#else
  len = 100 * sizeof(struct ifreq); /* initial buffer size guess */
  for ( ; ; ) {
    buf = malloc(len);
    ifc.ifc_len = len;
    ifc.ifc_buf = buf;
    if (ioctl(sockfd, SIOCGIFCONF, &ifc) < 0) {
      if (errno != EINVAL || lastlen != 0)
	perror("getifi_info ioctl error");
    } else {
      if (ifc.ifc_len == lastlen)
	break; /* success, len has not changed */
      lastlen = ifc.ifc_len;
    }
    len += 10 * sizeof(struct ifreq); /* increment */
    free(buf);
  }
#endif
  ifihead = NULL;
  ifipnext = &ifihead;
  lastname[0] = 0;

  for (ptr = buf; ptr < buf + ifc.ifc_len;) {
    ifr = (struct ifreq *) ptr;

#ifdef HAVE_SOCKADDR_SA_LEN
    len = max(sizeof(struct sockaddr), ifr->ifr_addr.sa_len);
#else
    switch(ifr->ifr_addr.sa_family) {
#ifdef IPV6
    case AF_INET6:
      len = sizeof(struct sockaddr_in6);
      break;
#endif
    case AF_INET:
    default:
      len = sizeof(struct sockaddr);
      break;
    }

#endif /* HAVE_SOCKADDR_SA_LEN */

    ptr += sizeof(ifr->ifr_name) + len; /* for next one in buffer */
    
    if (ifr->ifr_addr.sa_family != family)
      continue; /* ignore if not desired address family */
    
    myflags = 0;
    if ((cptr = strchr (ifr->ifr_name, ':')) != NULL) {
      *cptr = 0; /* replace colon with NULL */
    }
    if (strncmp(lastname, ifr->ifr_name, IFNAMSIZ ) == 0) {
      if (doaliases == 0) {
	continue; /* already processed this interface */
      }

      myflags = IFI_ALIAS;
    }
    memcpy(lastname, ifr->ifr_name, IFNAMSIZ);
    ifrcopy = *ifr;
    ioctl(sockfd, SIOCGIFFLAGS, &ifrcopy);
    flags = ifrcopy.ifr_flags;
    if ((flags & IFF_UP) == 0) {
      continue; /* ignore if interface not up */
    }
    ifi = malloc(sizeof(struct ifi_info));
    memset(ifi, 0, sizeof(struct ifi_info));
    *ifipnext = ifi; /* prev points to this new one */
    ifipnext = &ifi->ifi_next; /* pointer to next one goes here */
    ifi->ifi_flags = flags; /* IFF_xxx values */
    ifi->ifi_myflags = myflags; /* IFI_NAME */

    switch (ifr->ifr_addr.sa_family) {
    case AF_INET:
      sinptr = (struct sockaddr_in *)&ifr->ifr_addr;
      if(ifi->ifi_addr == NULL) {
	ifi->ifi_addr = calloc(1, sizeof(struct sockaddr_in));
	memcpy(ifi->ifi_addr, sinptr, sizeof(struct sockaddr_in));
#ifdef SIOCGIFBRDADDR
	if (flags & IFF_BROADCAST) {
	  ioctl(sockfd, SIOCGIFBRDADDR, &ifrcopy);
	  ifi->ifi_brdaddr = malloc(sizeof(struct sockaddr_in));
	  memset(ifi->ifi_brdaddr, 0, sizeof(struct sockaddr_in));
	  memcpy(ifi->ifi_brdaddr, sinptr, sizeof(struct sockaddr_in));
	  memcpy(ifi->ifi_brdaddr, sinptr, sizeof(struct sockaddr_in));
	}
#endif
#ifdef SIOCGIFDSTADDR
	if(flags & IFF_POINTOPOINT) {
	  ioctl(sockfd, SIOCGIFDSTADDR, &ifrcopy);
	  sinptr = (struct sockaddr_in *)&ifrcopy.ifr_dstaddr;
	  ifi->ifi_dstaddr = calloc(1, sizeof(struct sockaddr_in));
	  memcpy(ifi->ifi_dstaddr, sinptr, sizeof(struct sockaddr_in));
	}
#endif
      }
      break;
    default:
      break;
    }
  }
  free (buf);
  return(ifihead);    
}


static void 
free_ifi_info(struct ifi_info *ifihead)
{
  struct ifi_info *ifi, *ifinext;
  for (ifi = ifihead; ifi != NULL; ifi = ifinext) {
    if (ifi->ifi_addr != NULL)
      free(ifi->ifi_addr);
    if (ifi->ifi_brdaddr != NULL)
      free(ifi->ifi_brdaddr);
    if (ifi->ifi_dstaddr != NULL)
      free(ifi->ifi_dstaddr);
    ifinext = ifi->ifi_next; /* can't fetch ifi_next after free() */
    free(ifi); /* the ifi_info {} itself */
  }
}

struct ifi_info *InterfaceList = NULL;

#if 1
int 
XplGetInterfaceList(void) 
{
    if (InterfaceList != NULL) {
        free_ifi_info(InterfaceList);
    }

    InterfaceList = get_ifi_info(AF_INET, 1);

    return(0);
}

#else

static char *
Sock_ntop_host(struct sockaddr *sa, int len) 
{
  if (sa->sa_family == AF_INET) {
    struct sockaddr_in *sin = (struct sockaddr_in *)sa;

    return(inet_ntoa(sin->sin_addr));
  }
  return("NULL");

}

int 
XplGetInterfaceList(void) 
{
  struct sockaddr *sa;

	if (InterfaceList != NULL) {
		free_ifi_info(InterfaceList);
	}

	InterfaceList = get_ifi_info(AF_INET, 1);

	/* do we want to log this information ??? */

  {
    struct ifi_info *ifi;
    for (ifi = InterfaceList; ifi != NULL; ifi = ifi->ifi_next) {
      
      if (ifi->ifi_flags & IFF_UP) printf("UP ");
      if (ifi->ifi_flags & IFF_BROADCAST) printf("BCAST ");
      if (ifi->ifi_flags & IFF_MULTICAST) printf("MCAST ");
      if (ifi->ifi_flags & IFF_LOOPBACK) printf("LOOP ");
      if (ifi->ifi_flags & IFF_POINTOPOINT) printf("P2P ");
      printf(">\n");
      
      if ((sa = ifi->ifi_addr) != NULL) 
	printf("  IP addr: %s\n",
	       Sock_ntop_host(sa, sizeof(*sa)));
      if ( (sa = ifi->ifi_brdaddr) != NULL)
	printf("  broadcast addr: %s\n", 
	       Sock_ntop_host(sa, sizeof(*sa)));
      if ((sa = ifi -> ifi_dstaddr) != NULL) 
	printf(" destination addr: %s\n", 
	       Sock_ntop_host(sa, sizeof(*sa)));
    }
  }

  return(0);
}
#endif

int 
XplDestroyInterfaceList(void)
{
  if (InterfaceList) {
    free_ifi_info(InterfaceList);
    InterfaceList = NULL;
  }

  return(0);
}

BOOL
XplIsLocalIPAddress(unsigned long Address)
{
	struct ifi_info *ifi;
	struct sockaddr_in *sin;

        if (Address == htonl(INADDR_LOOPBACK)) {
            return TRUE;
        }

	for (ifi = InterfaceList; ifi != NULL; ifi=ifi->ifi_next) {
		/* sanity check address restrict to IPv 4 */
		if ((ifi->ifi_addr != NULL) && (ifi->ifi_addr->sa_family == AF_INET)) {
			sin = (struct sockaddr_in *)ifi->ifi_addr;
			if (sin->sin_addr.s_addr == Address) {
				return(TRUE);
			}
		}
	}

	return(FALSE);
}

__inline static BOOL
MakeSocketNonBlocking(int soc)
{
    long socketSettings;

    socketSettings = fcntl(soc, F_GETFL, NULL);
    if (socketSettings > -1) {
        socketSettings |= O_NONBLOCK;
        if (fcntl(soc, F_SETFL, socketSettings) > -1) {
            return(TRUE);            
        }
    }
    return(FALSE);
}

__inline static BOOL
MakeSocketBlocking(int soc)
{
    long socketSettings;

    socketSettings = fcntl(soc, F_GETFL, NULL);
    if (socketSettings > -1) {
        socketSettings &= (~O_NONBLOCK);
        if (fcntl(soc, F_SETFL, socketSettings) > -1) {
            return(TRUE);            
        }
    }
    return(FALSE);
}

int
XplIPConnectWithTimeout(IPSOCKET soc, struct sockaddr *addr, long addrSize, long timeout)
{
    long ccode;

    if (MakeSocketNonBlocking(soc)) {
        for (;;) {
            ccode = IPconnect(soc, addr, addrSize);
            if (ccode > -1) {
                if (MakeSocketBlocking(soc)) {
                    return(0);
                }

                return(-1);
            }

            if (timeout > 100) {
                XplDelay(100);
                timeout -= 100;
                continue;
            }

            if (timeout > 0) {
                XplDelay(timeout);
                timeout = 0;
                continue;
            }

            break;
        }
    }

    MakeSocketBlocking(soc);
    return(-1);
}

#endif /* SOLARIS || LINUX || S390RH */
