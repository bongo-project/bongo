
/* Automatically Generated - Do Not Edit */

#include <config.h>
#include "loggerp.h"
#include <stdio.h>

int
LoggerFormatMessage (int eventId, char *buf, unsigned int len, const char *var_S, const char *var_T, unsigned int var_1, unsigned int var_2, void *var_D, int var_X)
{
    struct in_addr x;
    
    switch (eventId) {
    case 0x00020001 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Bad password \"%s\" for account \"%s\" from %s", eventId, "REMOVED", var_S, inet_ntoa(x));
    case 0x00020002 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Unhandled request \"%s\" for account \"%s\" from %s", eventId, var_T, var_S, inet_ntoa(x));
    case 0x00020003 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Valid login for account \"%s\" from %s", eventId, var_S, inet_ntoa(x));
    case 0x00020004 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: %d failed logins for account \"%s\" from %s", eventId, var_2, var_S, inet_ntoa(x));
    case 0x00020005 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Feature disabled for account \"%s\" from %s", eventId, var_S, inet_ntoa(x));
    case 0x00020006 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Account \"%s\" does not exist; attempt from %s with password \"%s\"", eventId, var_S, inet_ntoa(x), "REMOVED");
    case 0x00020007 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Could not connect to NMAP server %s (code:%d)", eventId, inet_ntoa(x), var_2);
    case 0x00020008 :
        return snprintf (buf, len, "%08x: Could not allocated %d bytes (file:%s line:%d)", eventId, var_1, var_S, var_2);
    case 0x00020009 :
        return snprintf (buf, len, "%08x: Component not configured: %s", eventId, var_S);
    case 0x0002000A :
        return snprintf (buf, len, "%08x: Session limit (%d) reached", eventId, var_1);
    case 0x0002000B :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: User \"%s\" at %s changed password; result:%d", eventId, var_S, inet_ntoa(x), var_2);
    case 0x0002000C :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: User \"%s\" at %s changed secret Q/A (Old question: %s)", eventId, var_S, inet_ntoa(x), var_T);
    case 0x0002000D :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: User \"%s\" created from address %s using \"%s\"; assigned \"%s\" as the password.", eventId, var_S, inet_ntoa(x), var_T, "REMOVED");
    case 0x0002000E :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: User \"%s\" disabled from %s", eventId, var_S, inet_ntoa(x));
    case 0x0002000F :
        return snprintf (buf, len, "%08x: Memory allocated by file %s on line %d for %d bytes was not freed", eventId, var_S, var_1, var_2);
    case 0x00020010 :
        return snprintf (buf, len, "%08x: Null pointer used as destination by file %s on line %d", eventId, var_S, var_1);
    case 0x00020011 :
        return snprintf (buf, len, "%08x: Memory allocated by file %s on line %d overflowed", eventId, var_S, var_1);
    case 0x00020012 :
        return snprintf (buf, len, "%08x: Memory allocated by file %s on line %d was freed by file %s on line %d", eventId, var_S, var_1, var_T, var_2);
    case 0x00020013 :
        return snprintf (buf, len, "%08x: Memory allocated by file %s on line %d was questionably freed", eventId, var_S, var_1);
    case 0x00020014 :
        return snprintf (buf, len, "%08x: Questionable allocation requested by file %s on line %d for %d bytes", eventId, var_S, var_1, var_2);
    case 0x00020015 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Received message from \"%s\", size %d (IP:%s)", eventId, var_S, var_2, inet_ntoa(x));
    case 0x00020016 :
        return snprintf (buf, len, "%08x: Delivered message for \"%s\" to \"%s\", size %d; status %d", eventId, var_S, var_T, var_1, var_2);
    case 0x00020017 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Blocked connection from %s, reason %d (detail: %s)", eventId, inet_ntoa(x), var_2, var_S);
    case 0x00020018 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Blocked relay for recipient \"%s\" from address %s", eventId, var_S, inet_ntoa(x));
    case 0x00020019 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Relayed message for \"%s\" to recipient \"%s\" from address %s", eventId, var_S, var_T, inet_ntoa(x));
    case 0x0002001A :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Blocked relay for \"%s\" to recipient \"%s\" from address %s", eventId, var_S, var_T, inet_ntoa(x));
    case 0x0002001B :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: New connection from address %s", eventId, inet_ntoa(x));
    case 0x0002001C :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Registered NMAP Server %s for queue %d with SSL %s", eventId, inet_ntoa(x), var_2, var_S);
    case 0x0002001D :
        return snprintf (buf, len, "%08x: Failed to restart the anti-virus engine \"%s\"", eventId, var_S);
    case 0x0002001E :
        return snprintf (buf, len, "%08x: Have duplicate alias: user: \"%s\", alias: \"%s\"", eventId, var_S, var_T);
    case 0x0002001F :
        return snprintf (buf, len, "%08x: Open database \"%s\" returned %d", eventId, var_S, var_1);
    case 0x00020020 :
        return snprintf (buf, len, "%08x: Enumerating context \"%s\"", eventId, var_T);
    case 0x00020021 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Received bounce from \"%s\" at %s, auth from \"%s\"", eventId, var_S, inet_ntoa(x), var_T);
    case 0x00020022 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Blocked spam from \"%s\" at %s", eventId, var_S, inet_ntoa(x));
    case 0x00020023 :
        return snprintf (buf, len, "%08x: Blocked virus %s from \"%s\" to \"%s\"", eventId, var_T, (char*)var_D, var_S);
    case 0x00020024 :
        return snprintf (buf, len, "%08x: Forwarding for \"%s\" to \"%s\"", eventId, var_S, var_T);
    case 0x00020025 :
        return snprintf (buf, len, "%08x: Auto reply for \"%s\" to \"%s\"", eventId, var_S, var_T);
    case 0x00020026 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Received control request from \"%s\" at %s; auth from \"%s\"", eventId, var_S, inet_ntoa(x), var_T);
    case 0x00020027 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Processing error on request from \"%s\" at %s, queue ID: %s", eventId, var_S, inet_ntoa(x), var_T);
    case 0x00020028 :
        return snprintf (buf, len, "%08x: Blocked send by \"%s\" to \"%s\" reason %d", eventId, var_T, var_S, var_1);
    case 0x00020029 :
        return snprintf (buf, len, "%08x: Configuration for \"%s\": \"%s\"", eventId, var_S, var_T);
    case 0x0002002A :
        return snprintf (buf, len, "%08x: Configuration for \"%s\": %d", eventId, var_S, var_1);
    case 0x0002002B :
        return snprintf (buf, len, "%08x: Not enough memory; tuning parameters adjusted; concurrent limit: %d, sequential limit: %d", eventId, var_1, var_2);
    case 0x0002002C :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: New SSL connection from address %s", eventId, inet_ntoa(x));
    case 0x0002002D :
        return snprintf (buf, len, "%08x: Message for list \"%s\" received from \"%s\"", eventId, var_S, var_T);
    case 0x0002002E :
        return snprintf (buf, len, "%08x: Message from list \"%s\" for user \"%s\" delivered", eventId, var_S, var_T);
    case 0x0002002F :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Reloaded Queue Agent %s:%d for queue %d", eventId, inet_ntoa(x), *((int*)var_D), var_2);
    case 0x00020030 :
        return snprintf (buf, len, "%08x: Queue %d, entry \"%s\" from \"%s\"; auth from \"%s\"", eventId, var_1, var_S, var_T, (char*)var_D);
    case 0x00020031 :
        return snprintf (buf, len, "%08x: User \"%s\" quota of %d exceeds allotment of %d, message returned to \"%s\"", eventId, var_S, var_1, var_2, var_T);
    case 0x00020032 :
        return snprintf (buf, len, "%08x: \"%s\" \"%s\" %d %d", eventId, var_S, var_T, var_1, var_2);
    case 0x00020033 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Wrong system auth from IP Address %s", eventId, inet_ntoa(x));
    case 0x00020034 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Denied attempt to switch from user \"%s\" to user \"%s\" from IP Address %s", eventId, var_S, var_T, inet_ntoa(x));
    case 0x00020035 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Wrong auth user \"%s\" from IP Address %s", eventId, var_S, inet_ntoa(x));
    case 0x00020036 :
        return snprintf (buf, len, "%08x: Diskspace low for path \"%s\"; currently %d free blocks, minimum %d free blocks required", eventId, var_S, var_1, var_2);
    case 0x00020037 :
        return snprintf (buf, len, "%08x: Could not write to \"%s\" for user \"%s\"; line %d from %d", eventId, var_S, var_T, var_1, var_2);
    case 0x00020038 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Adding Queue Agent %s:%d for queue %d", eventId, inet_ntoa(x), *((int*)var_D), var_2);
    case 0x00020039 :
        return snprintf (buf, len, "%08x: Hostname \"%s\" is unusable, specified by user \"%s\"", eventId, var_S, var_T);
    case 0x0002003A :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: User \"%s\" attempted to proxy %s (the localhost)", eventId, var_S, inet_ntoa(x));
    case 0x0002003B :
        return snprintf (buf, len, "%08x: User \"%s\" received NMAP error %d (detail: %s)", eventId, var_S, var_1, var_T);
    case 0x0002003C :
        return snprintf (buf, len, "%08x: DDB initialization failed (detail: %s)", eventId, var_S);
    case 0x0002003D :
        return snprintf (buf, len, "%08x: Hostname \"%s\" is not allowed, specified by user \"%s\"", eventId, var_S, var_T);
    case 0x0002003E :
        return snprintf (buf, len, "%08x: User \"%s\" executing rule \"%s\"", eventId, var_S, var_T);
    case 0x0002003F :
        return snprintf (buf, len, "%08x: Removing recipient \"%s\"", eventId, var_S);
    case 0x00020040 :
        return snprintf (buf, len, "%08x: Forwarding from \"%s\" to \"%s\"", eventId, var_S, var_T);
    case 0x00020041 :
        return snprintf (buf, len, "%08x: Copy from \"%s\"", eventId, var_S);
    case 0x00020042 :
        return snprintf (buf, len, "%08x: Moving into mailbox \"%s:%s\"", eventId, var_S, var_T);
    case 0x00020043 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Removed Queue Agent %s:%d, queue %d; agent re-registered", eventId, inet_ntoa(x), *((int*)var_D), var_2);
    case 0x00020044 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Removed Queue Agent %s:%d, queue %d", eventId, inet_ntoa(x), *((int*)var_D), var_2);
    case 0x00020045 :
        return snprintf (buf, len, "%08x: Find \"%s\"  returned %d", eventId, var_S, var_1);
    case 0x00020046 :
        return snprintf (buf, len, "%08x: Insert \"%s\" with alias \"%s\" returned %d", eventId, var_S, var_T, var_1);
    case 0x00020047 :
        return snprintf (buf, len, "%08x: Close database \"%s\" returned %d", eventId, var_S, var_1);
    case 0x00020048 :
        return snprintf (buf, len, "%08x: Storing item from \"%s\" for \"%s\" with size of %d; location %s", eventId, var_T, var_S, var_1, (char*)var_D);
    case 0x00020049 :
        return snprintf (buf, len, "%08x: Insert \"%s\" into database returned %d", eventId, var_S, var_1);
    case 0x0002004A :
        return snprintf (buf, len, "%08x: Create database \"%s\" returned %d", eventId, var_S, var_1);
    case 0x0002004B :
        return snprintf (buf, len, "%08x: Processing queue %d entry \"%d\"", eventId, var_1, var_2);
    case 0x0002004C :
        x.s_addr = var_2;
        return snprintf (buf, len, "%08x: Recipient \"%s\" was not found for queue %d from \"%s\" at IP Address %s", eventId, var_S, var_1, var_T, inet_ntoa(x));
    case 0x0002004D :
        return snprintf (buf, len, "%08x: Sending queue %d entry \"%s\" via NMAP link to %d for \"%s\"", eventId, var_1, var_S, var_2, var_T);
    case 0x0002004E :
        return snprintf (buf, len, "%08x: Failed to delivery for queue %d, entry \"%s\"; status %d", eventId, var_1, var_S, var_2);
    case 0x0002004F :
        x.s_addr = var_2;
        return snprintf (buf, len, "%08x: Queue %d entry \"%s\" failed to connect to %s:%d", eventId, var_1, var_S, inet_ntoa(x), *((int*)var_D));
    case 0x00020050 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Queue agent %s:%d failed to process queue %d entry \"%s\"", eventId, inet_ntoa(x), *((int*)var_D), var_2, var_S);
    case 0x00020051 :
        return snprintf (buf, len, "%08x: Missing strings.txt for language %d", eventId, var_1);
    case 0x00020052 :
        return snprintf (buf, len, "%08x: \"%s\" is missing strings; found %d, expected %d", eventId, var_S, var_1, var_2);
    case 0x00020053 :
        return snprintf (buf, len, "%08x: Sent digest for list \"%s\"", eventId, var_S);
    case 0x00020054 :
        return snprintf (buf, len, "%08x: Unable to create a socket; file \"%s\", line %d", eventId, var_S, var_1);
    case 0x00020055 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Unable to connect to the Symantec CarrierScan host at %s", eventId, inet_ntoa(x));
    case 0x00020056 :
        return snprintf (buf, len, "%08x: User \"%s\" has an illegal e-mail address of \"%s\"", eventId, var_S, var_T);
    case 0x00020057 :
        return snprintf (buf, len, "%08x: Open strings file for language %d returned %s", eventId, var_1, var_S);
    case 0x00020058 :
        return snprintf (buf, len, "%08x: Delivery retry needed for \"%s\" to \"%s\", size %d; status %d", eventId, var_S, var_T, var_1, var_2);
    case 0x00020059 :
        return snprintf (buf, len, "%08x: Delivery failed for \"%s\" to \"%s\", size %d; st", eventId, var_S, var_T, var_1);
    case 0x0002005A :
        return snprintf (buf, len, "%08x: Memory allocated by file %s on line %d will overflow due to write from %s on line %d; attempted to write %d bytes", eventId, var_S, var_1, var_T, var_2, *((int*)var_D));
    case 0x0002005B :
        return snprintf (buf, len, "%08x: Null pointer used as source by file %s on line %d", eventId, var_S, var_1);
    case 0x0002005C :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: IP Address %s blocked due to message from \"%s\" with %d recipients", eventId, inet_ntoa(x), var_S, var_2);
    case 0x0002005D :
        return snprintf (buf, len, "%08x: accept() failed with error %d; SSL connection state %d", eventId, var_1, var_2);
    case 0x0002005E :
        return snprintf (buf, len, "%08x: Index file \"%s\" for user \"%s\", index %d, is invalid", eventId, var_T, var_S, var_1);
    case 0x0002005F :
        return snprintf (buf, len, "%08x: NMAP failed to allocate %d bytes for user \"%s\", mailbox \"%s\"; unloading", eventId, var_1, var_S, var_T);
    case 0x00020060 :
        return snprintf (buf, len, "%08x: File %s failed to open on line %d", eventId, var_S, var_1);
    case 0x00020061 :
        return snprintf (buf, len, "%08x: %s set errno %d on line %d", eventId, var_S, var_2, var_1);
    case 0x00020062 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Address %s timed out, connection was alive for %d seconds", eventId, inet_ntoa(x), var_2);
    case 0x00020063 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Connection error from %s, connection was alive for %d seconds", eventId, inet_ntoa(x), var_2);
    case 0x00020064 :
        return snprintf (buf, len, "%08x: Unable to lock queue entry %d; lock table full", eventId, var_1);
    case 0x00020065 :
        return snprintf (buf, len, "%08x: Agent heartbeat.", eventId);
    case 0x00020066 :
        return snprintf (buf, len, "%08x: Mailbox file \"%s\" for user \"%s\", Message %d, is invalid", eventId, var_T, var_S, var_1);
    case 0x00020067 :
        return snprintf (buf, len, "%08x: Proxy heartbeat.", eventId);
    case 0x00020068 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: DNS resolved %s to your server (%s). ", eventId, var_S, inet_ntoa(x));
    case 0x00020069 :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: DNS resolved %s to your server (%s). Configure Bongo to accept mail for this domain or change the DNS record.", eventId, var_S, inet_ntoa(x));
    case 0x0002006A :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Message from %s had %d unknown users.", eventId, inet_ntoa(x), var_2);
    case 0x0002006B :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Attempt to connect to %s (%s) failed! Reason: %s", eventId, inet_ntoa(x), var_T, var_S);
    case 0x0002006C :
        return snprintf (buf, len, "%08x: SMTP server returned greeting %d %s  Recipient: %s", eventId, var_1, var_S, var_T);
    case 0x0002006D :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Protocol negotiation with %s (%s) failed! Reason: %s", eventId, inet_ntoa(x), var_S, var_T);
    case 0x0002006E :
        x.s_addr = var_1;
        return snprintf (buf, len, "%08x: Network communication with %s (%s) failed! Reason: %s", eventId, inet_ntoa(x), var_S, var_T);
    case 0x0002006F :
        return snprintf (buf, len, "%08x: Proxied %d messages for \"%s\" from \"%s\"; %d KiloBytes", eventId, var_1, var_S, var_T, var_2);
    case 0x00020070 :
        return snprintf (buf, len, "%08x: Server Shutting Down: state=%d errno=%d", eventId, var_1, var_2);
    case 0x00020071 :
        return snprintf (buf, len, "%08x: Could not drop to unprivileged user '%s'", eventId, var_S);
    case 0x00020072 :
        return snprintf (buf, len, "%08x: Could not bind to %sport %d", eventId, var_S, var_1);
    }
return -1;
}
