# Copyright (C) 2002-2006 Python Software Foundation
# Author: Barry Warsaw
# Contact: email-sig@python.org

"""Base class for MIME type messages that are not multipart."""

__all__ = ['MIMENonMultipart']

from bongo.external.email import errors
from bongo.external.email.mime.base import MIMEBase



class MIMENonMultipart(MIMEBase):
    """Base class for MIME multipart/* type messages."""

    __pychecker__ = 'unusednames=payload'

    def attach(self, payload):
        # The public API prohibits attaching multiple subparts to MIMEBase
        # derived subtypes since none of them are, by definition, of content
        # type multipart/*
        raise errors.MultipartConversionError(
            'Cannot attach additional subparts to non-multipart/*')

    del __pychecker__
