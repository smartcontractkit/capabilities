// SPDX-License-Identifier: MIT
pragma solidity ^0.8.19;

// PLEX-1612 - This contract will be removed and we will use the actual one deployed in prod for CRE EVM but first we need to be able to create a valid report.
contract KeystoneForwarderTest {
  error InvalidReport();
  error FaultToleranceMustBePositive();
  error UnauthorizedForwarder();
  error InsufficientGasForRouting(bytes32 transmissionId);
  error AlreadyAttempted(bytes32 transmissionId);

  struct Transmission {
    address transmitter;
    bool invalidReceiver;
    bool success;
    uint80 gasLimit;
  }

  enum TransmissionState {
    NOT_ATTEMPTED,
    SUCCEEDED,
    INVALID_RECEIVER,
    FAILED
  }

  struct TransmissionInfo {
    bytes32 transmissionId;
    TransmissionState state;
    address transmitter;
    bool invalidReceiver;
    bool success;
    uint80 gasLimit;
  }

  event ReportProcessed(
    address indexed receiver,
    bytes32 indexed workflowExecutionId,
    bytes2 indexed reportId,
    bool result
  );

  constructor() {
    s_forwarders[address(this)] = true;
  }

  uint256 internal constant MAX_ORACLES = 31;
  uint256 internal constant METADATA_LENGTH = 109;
  uint256 internal constant FORWARDER_METADATA_LENGTH = 45;
  uint256 internal constant SIGNATURE_LENGTH = 65;

  uint256 internal constant INTERNAL_GAS_REQUIREMENTS_AFTER_REPORT = 5_000;
  uint256 internal constant INTERNAL_GAS_REQUIREMENTS = 25_000 + INTERNAL_GAS_REQUIREMENTS_AFTER_REPORT;
  uint256 internal constant MINIMUM_GAS_LIMIT = INTERNAL_GAS_REQUIREMENTS + 30_000 * 3 + 10_000;

  mapping(address forwarder => bool isForwarder) internal s_forwarders;
  mapping(bytes32 transmissionId => Transmission transmission) internal s_transmissions;

  function addForwarder(address forwarder) external {
    s_forwarders[forwarder] = true;
  }

  function removeForwarder(address forwarder) external {
    s_forwarders[forwarder] = false;
  }

  function route(
    bytes32 transmissionId,
    address transmitter
  ) public returns (bool) {
    if (!s_forwarders[msg.sender]) revert UnauthorizedForwarder();

    uint256 gasLimit = gasleft() - INTERNAL_GAS_REQUIREMENTS;
    if (gasLimit < MINIMUM_GAS_LIMIT) revert InsufficientGasForRouting(transmissionId);

    Transmission memory transmission = s_transmissions[transmissionId];
    if (transmission.success || transmission.invalidReceiver) revert AlreadyAttempted(transmissionId);

    s_transmissions[transmissionId].transmitter = transmitter;
    s_transmissions[transmissionId].gasLimit = uint80(gasLimit);

    s_transmissions[transmissionId].invalidReceiver = true;
    return false;
  }

  function getTransmissionId(
    address receiver,
    bytes32 workflowExecutionId,
    bytes2 reportId
  ) public pure returns (bytes32) {
    // This is slightly cheaper compared to `keccak256(abi.encode(receiver, workflowExecutionId, reportId));`
    return keccak256(bytes.concat(bytes20(uint160(receiver)), workflowExecutionId, reportId));
  }

  function getTransmissionInfo(
    address receiver,
    bytes32 workflowExecutionId,
    bytes2 reportId
  ) external view returns (TransmissionInfo memory) {
    bytes32 transmissionId = getTransmissionId(receiver, workflowExecutionId, reportId);

    Transmission memory transmission = s_transmissions[transmissionId];

    TransmissionState state;

    if (transmission.transmitter == address(0)) {
      state = TransmissionState.NOT_ATTEMPTED;
    } else if (transmission.invalidReceiver) {
      state = TransmissionState.INVALID_RECEIVER;
    } else {
      state = transmission.success ? TransmissionState.SUCCEEDED : TransmissionState.FAILED;
    }

    return
      TransmissionInfo({
        gasLimit: transmission.gasLimit,
        invalidReceiver: transmission.invalidReceiver,
        state: state,
        success: transmission.success,
        transmissionId: transmissionId,
        transmitter: transmission.transmitter
      });
  }

  function getTransmitter(
    address receiver,
    bytes32 workflowExecutionId,
    bytes2 reportId
  ) external view returns (address) {
    return s_transmissions[getTransmissionId(receiver, workflowExecutionId, reportId)].transmitter;
  }

  function isForwarder(address forwarder) external view returns (bool) {
    return s_forwarders[forwarder];
  }


  // send a report to receiver
  function report(
    address receiver,
    bytes calldata rawReport,
    bytes calldata reportContext,
    bytes[] calldata signatures
  ) external {
    if (rawReport.length < METADATA_LENGTH) {
      revert InvalidReport();
    }

    bytes32 workflowExecutionId;
    bytes2 reportId;
    uint64 configId;
    (workflowExecutionId, configId, reportId) = _getMetadata(rawReport);
   
    bool success = this.route(
      getTransmissionId(receiver, workflowExecutionId, reportId),
      msg.sender
    );

    emit ReportProcessed(receiver, workflowExecutionId, reportId, success);
  }

  // solhint-disable-next-line chainlink-solidity/explicit-returns
  function _getMetadata(
    bytes memory rawReport
  ) internal pure returns (bytes32 workflowExecutionId, uint64 configId, bytes2 reportId) {
    assembly {
      workflowExecutionId := mload(add(rawReport, 33))
      configId := shr(mul(24, 8), mload(add(rawReport, 69)))
      reportId := mload(add(rawReport, 139))
    }
  }
}
