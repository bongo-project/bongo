## Sundial mod_python handler class.
#
#  @author Jonny Lamb <jonnylamb@jonnylamb.com>
#  @copyright 2007 Jonny Lamb
#  @license GNU GPL v2

import bongo.commonweb

## Default SundialHandler class.
class SundialHandler(object):
    ## Returns whether the handler requires authentication.
    #  @param self The object pointer.
    #  @param rp The SundialPath instance for the current request.
    def NeedsAuth(self, rp):
        return True

    ## Looks at the path of the request. By default, does nothing.
    #  @param self The object pointer.
    #  @param rp The SundialPath instance for the current request.
    def ParsePath(self, rp):
        pass

    ## Entry point for the handler.
    #  @param self The object pointer.
    #  @param req The HttpRequest instance for the current request.
    #  @param rp The SundialPath instance for the current request.
    def index(self, req, rp):
        return bongo.commonweb.OK
