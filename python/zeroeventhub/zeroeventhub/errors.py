"""
This module defines a custom error class, APIError, and several specific error instances that can
be used to indicate specific error conditions in an API.

The specific error instances (e.g. `ErrCursorsMissing`) are pre-defined instances
of the APIError class, with specific error messages and codes. They can be used to indicate
specific error conditions in your API.
"""


class APIError(ValueError):
    """
    APIError has two attributes, `message` and `code`, which are set during initialization. The
    `message` attribute represents a human-readable error message, and the `code` attribute is an
    HTTP status code that can be used to indicate the type of error that occurred.
    """

    def __init__(self, message: str, code: int) -> None:
        self.message = message
        self.code = code

    def __str__(self) -> str:
        return self.message

    def status(self) -> int:
        """Return the HTTP status code associated with this APIError."""
        return self.code


ErrCursorsMissing = APIError("cursors are missing", 400)
