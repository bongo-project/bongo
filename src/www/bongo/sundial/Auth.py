## Sundial authentication functions.
#
#  @author Jonny Lamb <jonnylamb@jonnylamb.com>
#  @copyright 2007 Jonny Lamb
#  @license GNU GPL v2

from bongo.store.StoreClient import StoreClient
import bongo.commonweb

## Function to actually test the credentials provided by a user.
#  @param req The HttpRequest instance for the current request.
def AcceptCredentials(req):
    credUser = req.user
    credPass = req.get_basic_auth_pw()

    if credUser is None and credPass is None:
        # no session
        return False

    # Try and authenticate by connecting to the store.
    client = None
    try:
        client = StoreClient(credUser, credUser, authPassword=credPass)
    finally:
        if client:
            client.Quit()
            return True

    return False

## Authentication handler for Sundial.
#  @param req The HttpRequest instance for the current request
#
# Tests the credentials the user provided by calling AcceptCredentials.
# Upon a success or failure, return the appropriate HTTP error code.
def authenhandler(req):
    if AcceptCredentials(req):
#        req.log.debug("successful auth")
        return bongo.commonweb.HTTP_OK

    return bongo.commonweb.HTTP_UNAUTHORIZED
