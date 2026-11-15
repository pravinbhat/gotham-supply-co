class ArmoryError(Exception):
    """Business error with a stable code; rendered as {"error": code, "message": ...}."""

    def __init__(self, status: int, code: str, message: str):
        super().__init__(message)
        self.status = status
        self.code = code
        self.message = message
