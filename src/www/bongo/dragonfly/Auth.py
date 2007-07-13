import BongoCookie as Cookie
from bongo.admin.BongoSession import BongoSession as Session
import base64
import logging
import pprint
import time

from libbongo.libs import mdb
from libbongo.libs import msgapi
from bongo.store.StoreClient import StoreClient
from bongo.store.StoreConnection import StoreConnection
import bongo
import bongo.dragonfly

AUTH_TOKEN_NAME = "BongoAuthToken"

def GetCredentials(req):
    basic = "BASIC"
    header = "X-Bongo-Authorization"

    autz = req.headers_in.get(header, None)
    if autz is None:
        header = "Authorization"
        autz = req.headers_in.get(header, None)

    if autz is not None and autz.upper().startswith(basic):
        req.log.debug("got credentials from %s header" % header)
        return autz[len(basic)+1:]

    # if content-type is multipart/form-data, look at the form and get
    # the authorization field
    if req.form is not None:
        if req.form.has_key("authorization"):
            autz = req.form["authorization"].value
            if autz is not None and autz.upper().startswith(basic):
                req.log.debug("got credentials from form-data")
                return autz[len(basic)+1:]

def CheckAuthCookie(username, cookie):
    addr = msgapi.FindUserNmap(username)
    if addr is None:
        # this is likely because the user does not exist
        return False;
    host, port = addr

    ret = False

    conn = None
    try:
        conn = StoreConnection(host, port, systemAuth=False)
        ret = conn.CookieAuth(username, cookie)
    finally:
        if conn:
            conn.Close()

    return ret

def CheckUserPass(username, password):
    # shortcut some failure cases, so we don't have to hit MDB
    if username is None or password is None:
        return False
    dn = msgapi.FindObject(username)
    handle = msgapi.DirectoryHandle()
    return mdb.mdb_VerifyPassword(handle, dn, password)

def GetUserPass(creds):
    return base64.decodestring(creds).split(":")

def EncodeUserPass(username, password):
    return base64.encodestring(username + ":" + password).strip()

def CreateStoreCookie(req, username, password):
    store = None
    try:
        store = StoreClient(username, username, authPassword=password)
        cookie = store.CookieNew(604800)
        store.Quit()
    except:
        req.log.debug("Exception creating store cookie for %s", username, exc_info=1)
        if store is not None:
            store.Quit()
        return None

    return cookie

def GetAuthCookieName(username):
    return "%s.%s" % (AUTH_TOKEN_NAME, username)

def AcceptCredentials(req):
    authCookie = None
    storeCookie = None
    authCookieName = None
    currentUser = None

    credUser = credPass = None

    authCreds = GetCredentials(req)
    reqCookies = Cookie.get_cookies(req)

    req.authCookie = None

    # print the headers, after cleaning out any authorization information
    headers = {}
    headers.update(req.headers_in)
    for key, value in headers.items():
        if key.lower().find("authorization") != -1:
            headers[key] = "********"
    req.log.debug("Headers are: %s", pprint.pformat(headers))
    req.log.debug("Cookies are: %s", reqCookies)

    # try to find the current user
    if authCreds:
        credUser, credPass = GetUserPass(authCreds)

    if credUser:
        currentUser = credUser
    else:
        currentUserCookie = reqCookies.get("currentUser".lower(), None)

        if currentUserCookie is not None:
            currentUser = currentUserCookie.value

    if currentUser:
        authCookieName = GetAuthCookieName(currentUser)
        req.log.debug("currentUser is %s", currentUser)
    else:
        req.log.debug("Unable to figure out currentUser")

    # different cookie libs give us different results?!
    if authCookieName:
        authCookie = reqCookies.get(authCookieName, None)
        if not authCookie:
            authCookie = reqCookies.get(authCookieName.lower(), None)
        if not authCookie:
            req.log.debug("Unable to get cookie %s for %s", authCookieName, currentUser)

    if not authCreds and not authCookie:
        # no creds and no cookie -> anonymous auth, so allow the
        # request to continue and let the store check acls
        req.log.debug("no auth header and no auth cookie -> anonymous auth")
        return True

    if credUser and credPass:
        if not CheckUserPass(credUser, credPass):
            req.log.debug("invalid auth credentials")
            return False

        # get a new cookie from the user's store, expire in seven days
        newCookie = CreateStoreCookie(req, credUser, credPass)

        if newCookie is None:
            req.log.debug("couldn't create a store cookie for %s", credUser)
            return False

        authCookieName = GetAuthCookieName(credUser)

        req.log.debug("adding auth cookie: %s", authCookieName)
        Cookie.add_cookie(req, authCookieName, newCookie, path="/")

        storeCookie = newCookie
    elif authCookie:
        if not CheckAuthCookie(currentUser, authCookie.value):
            req.log.debug("invalid auth cookie")

            # delete the cookie by setting its expiration date to a week ago
            Cookie.add_cookie(req, authCookieName, "", path="/",
                              expires=time.time()-604800)
            return False

        storeCookie = authCookie.value
    else:
        req.log.debug("no auth credentials or cookie found")
        return False

    req.log.debug("auth successful")

    ver = req.headers_in.get("X-Bongo-Agent", None)
    if ver is None:
        req.log.debug("adding currentUser cookie")
        # expire in one hour
        Cookie.add_cookie(req, "currentUser", currentUser,
                          path=req.uri, expires=time.time()+3600)

    req.user = currentUser
    req.authCookie = storeCookie

    return True

def DenyAccess(req):
    # add our authentication headers, but not for our XmlHTTPRequests
    if not req.headers_in.has_key("X-Bongo-Agent"):
        req.headers_out["WWW-Authenticate"] = "Basic realm=\"Bongo\""

    return bongo.dragonfly.HTTP_UNAUTHORIZED

def authenhandler(req):
    if AcceptCredentials(req):
        return bongo.dragonfly.HTTP_OK

    return DenyAccess(req)
