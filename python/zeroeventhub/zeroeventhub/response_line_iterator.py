"""httpx response line iterator."""

import collections.abc

import httpx


async def aiter_lines(
    response: httpx.Response, newline_chars: str | None = None
) -> collections.abc.AsyncIterator[str]:
    """Iterate through the lines in the response, respecting the given newline characters."""
    decoder = LineDecoder(newline_chars)
    async for text in response.aiter_text():
        for line in decoder.decode(text):
            yield line
    for line in decoder.flush():
        yield line


def splitlines(text: str, newline_chars: str) -> collections.abc.Iterator[str]:
    """Split lines keeping the endings, then stitch them back together
    if the end isn't in the given list of newline characters.
    """
    lines = text.splitlines(keepends=True)
    buffer = ""
    yielded = False
    for line in lines:
        if line and line[-1] in newline_chars:
            yield buffer + line[0:-1]
            buffer = ""
            yielded = True
        else:
            buffer += line
    if buffer or not yielded:
        yield buffer


# LineDecoder taken from
# https://github.com/encode/httpx/blob/3ba5fe0d7ac70222590e759c31442b1cab263791/httpx/_decoders.py
# #L258C1-L313C1
# and modified to take NEWLINE_CHARS as a parameter.
#
# httpx has the following license file:
#
# Copyright © 2019, [Encode OSS Ltd](https://www.encode.io/).
# All rights reserved.
# Redistribution and use in source and binary forms, with or without modification, are permitted
# provided that the following conditions are met:
# * Redistributions of source code must retain the above copyright notice, this list of conditions
#   and the following disclaimer.
# * Redistributions in binary form must reproduce the above copyright notice, this list of
#   conditions and the following disclaimer in the documentation and/or other materials provided
#   with the distribution.
# * Neither the name of the copyright holder nor the names of its contributors may be used to
#   endorse or promote products derived from this software without specific prior written
#   permission.
# THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND ANY EXPRESS OR
# IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND
# FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR
# CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
# DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
# DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY,
# WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY
# WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
class LineDecoder:
    """
    Handles incrementally reading lines from text.

    Has the same behaviour as the stdllib splitlines, but handling the input iteratively
    and allows restricting which characters are considered newline characters.
    """

    def __init__(self, newline_chars: str | None = None) -> None:
        """Initialize the line decoder with the given set of characters which denote newlines."""
        self.buffer: list[str] = []
        self.trailing_cr: bool = False
        # See https://docs.python.org/3/library/stdtypes.html#str.splitlines
        self.newline_chars = newline_chars or "\n\r\x0b\x0c\x1c\x1d\x1e\x85\u2028\u2029"

    def decode(self, text: str) -> list[str]:
        """Decode the given text into a series of lines."""
        # We always push a trailing `\r` into the next decode iteration.
        if self.trailing_cr:
            text = "\r" + text
            self.trailing_cr = False
        if text.endswith("\r"):
            self.trailing_cr = True
            text = text[:-1]

        if not text:
            return []

        trailing_newline = text[-1] in self.newline_chars
        lines = list(splitlines(text, self.newline_chars))

        if len(lines) == 1 and not trailing_newline:
            # No new lines, buffer the input and continue.
            self.buffer.append(lines[0])
            return []

        if self.buffer:
            # Include any existing buffer in the first portion of the
            # splitlines result.
            lines = ["".join(self.buffer) + lines[0]] + lines[1:]
            self.buffer = []

        if not trailing_newline:
            # If the last segment of splitlines is not newline terminated,
            # then drop it from our output and start a new buffer.
            self.buffer = [lines.pop()]

        return lines

    def flush(self) -> list[str]:
        """Flush the line buffer."""
        if not self.buffer and not self.trailing_cr:
            return []

        lines = ["".join(self.buffer)]
        self.buffer = []
        self.trailing_cr = False
        return lines
